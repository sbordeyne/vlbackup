package http_handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/op/go-logging"
	"github.com/sbordeyne/vlbackup/pkg/cli"
	"github.com/sbordeyne/vlbackup/pkg/metrics"
	"github.com/sbordeyne/vlbackup/pkg/objstore"
	"github.com/sbordeyne/vlbackup/pkg/transfer"
	"github.com/sbordeyne/vlbackup/pkg/victoriametrics"
)

var log = logging.MustGetLogger("vlbackup.http_handler")
var format = logging.MustStringFormatter(
	`%{color}%{time:15:04:05.000} %{shortfunc} ▶ %{level:.4s} %{id:03x}%{color:reset} %{message}`,
)

type TriggerRequestBody struct {
	PartitionPrefix string `json:"partition_prefix"`
	DestinationURL  string `json:"destination_url"`
}

func parseRequestBody(body io.ReadCloser) (TriggerRequestBody, error) {
	// Decode the body into struct `TriggerRequestBody`, it should have 2 params:
	// partition_prefix, used to dictate which snapshot to take, its optional, if not
	// found, it'll default to yesterday UTC
	// destination_url on the other hand is required and will be an URL in the form
	// scheme://bucket_name/pathprefix/ (e.g. gs:// or s3://)
	decoder := json.NewDecoder(body)
	yesterday := time.Now().Add(-time.Hour * 24).Format("20060102")
	parsed := TriggerRequestBody{
		PartitionPrefix: yesterday,
	}
	err := decoder.Decode(&parsed)
	if err != nil {
		return parsed, err
	}
	return parsed, nil
}

func handleError(w http.ResponseWriter, err error, partitionPrefix string, metrics *metrics.Metrics, statusCode int) {
	stack := debug.Stack()
	fmt.Println(string(stack))
	fmt.Printf("error: %#v\n", err)
	writeJSON(w, statusCode, map[string]string{"error": err.Error()})
	metrics.SnapshotCount.WithLabelValues(partitionPrefix, "false").Inc()
}

// partitionFromSnapshotPath extracts the partition name (YYYYMMDD) from a
// VictoriaLogs snapshot path like <dataPath>/partitions/<YYYYMMDD>/snapshots/<id>,
// falling back to the path's base name if the layout is unexpected.
func partitionFromSnapshotPath(p string) string {
	segments := strings.Split(filepath.ToSlash(p), "/")
	for i, segment := range segments {
		if segment == "partitions" && i+1 < len(segments) {
			return segments[i+1]
		}
	}
	return filepath.Base(p)
}

func TriggerHandlerFactory(args cli.Args, metrics *metrics.Metrics) func(w http.ResponseWriter, r *http.Request) {
	backend := logging.NewLogBackend(os.Stdout, "", 0)
	formatter := logging.NewBackendFormatter(backend, format)
	logging.SetBackend(formatter)
	return func(w http.ResponseWriter, r *http.Request) {
		startTime := time.Now()
		body, err := parseRequestBody(r.Body)
		if err != nil {
			handleError(w, err, body.PartitionPrefix, metrics, http.StatusBadRequest)
			return
		}
		metrics.SnapshotDuration.WithLabelValues(body.PartitionPrefix, "parse_request_body").Observe(time.Since(startTime).Abs().Seconds())
		repo, keyPrefix, err := objstore.Open(r.Context(), body.DestinationURL)
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, objstore.ErrUnsupportedScheme) {
				status = http.StatusBadRequest
			}
			handleError(w, err, body.PartitionPrefix, metrics, status)
			return
		}
		defer func() { _ = repo.Close() }()
		log.Infof("opened storage repository for destination %s", body.DestinationURL)
		vmClient, err := victoriametrics.NewClient(r.Context(), args.VictoriaLogsURL.String())
		log.Info("initialized vmClient")
		if err != nil {
			handleError(w, err, body.PartitionPrefix, metrics, http.StatusInternalServerError)
			return
		}
		snapshotPaths, err := vmClient.CreateSnapshot(body.PartitionPrefix, args.VictoriaLogsAuthKey)
		log.Infof("Created snapshot, got paths %#v", snapshotPaths)
		if err != nil {
			handleError(w, err, body.PartitionPrefix, metrics, http.StatusInternalServerError)
			return
		}
		if len(snapshotPaths) == 0 {
			w.WriteHeader(http.StatusNoContent)
			_, _ = w.Write([]byte("Snapshot created but no paths returned, nothing to copy"))
			metrics.SnapshotCount.WithLabelValues(body.PartitionPrefix, "true").Inc()
			return
		}
		for _, snapshotPath := range snapshotPaths {
			key := path.Join(keyPrefix, partitionFromSnapshotPath(snapshotPath)+".tar.gz")
			log.Infof("Uploading snapshot %s to %s as %s", snapshotPath, body.DestinationURL, key)
			pr, pw := io.Pipe()
			go func() {
				pw.CloseWithError(transfer.StreamDir(transfer.SnapshotPathResolver(snapshotPath), pw))
			}()
			if err := repo.Upload(r.Context(), key, pr); err != nil {
				pr.CloseWithError(err)
				handleError(w, err, body.PartitionPrefix, metrics, http.StatusInternalServerError)
				return
			}
			log.Infof("Deleting snapshot %s", snapshotPath)
			err = vmClient.DeleteSnapshot(snapshotPath, args.VictoriaLogsAuthKey)
			if err != nil {
				handleError(w, err, body.PartitionPrefix, metrics, http.StatusInternalServerError)
				return
			}
		}

		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("OK"))
		metrics.SnapshotCount.WithLabelValues(body.PartitionPrefix, "true").Inc()
	}
}
