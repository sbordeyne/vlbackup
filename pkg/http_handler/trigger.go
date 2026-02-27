package http_handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"runtime/debug"
	"time"

	"cloud.google.com/go/storage"
	"github.com/op/go-logging"
	"github.com/sbordeyne/vlbackup/pkg/cli"
	"github.com/sbordeyne/vlbackup/pkg/metrics"
	"github.com/sbordeyne/vlbackup/pkg/victoriametrics"
)

var log = logging.MustGetLogger("vlbackup.http_handler")
var format = logging.MustStringFormatter(
	`%{color}%{time:15:04:05.000} %{shortfunc} ▶ %{level:.4s} %{id:03x}%{color:reset} %{message}`,
)


type TriggerRequestBody struct {
	PartitionPrefix string `json:"partition_prefix"`
	DestinationURL string `json:"destination_url"`
}

func parseRequestBody(body io.ReadCloser) (TriggerRequestBody, error) {
	// Decode the body into struct `TriggerRequestBody`, it should have 2 params:
	// partition_prefix, used to dictate which snapshot to take, its optional, if not
	// found, it'll default to yesterday UTC
	// destination_url on the other hand is required and will be an URL in the form
	// gs://bucket_name/pathprefix/
	decoder := json.NewDecoder(body)
	yesterday := time.Now().Add(-time.Hour * 24).Format("20060102")
	parsed := TriggerRequestBody{
		PartitionPrefix: yesterday,
	}
	err := decoder.Decode(&parsed)
	if err != nil {
		return parsed, err;
	}
	return parsed, nil
}

func handleError(w http.ResponseWriter, err error, partitionPrefix string, metrics *metrics.Metrics, statusCode int) {
	stack := debug.Stack()
	fmt.Println(string(stack))
	fmt.Printf("error: %#v\n", err)
	fmt.Fprintf(w, "{\"error\": \"%#v\"}", err)

	w.WriteHeader(statusCode)
	metrics.SnapshotCount.WithLabelValues(partitionPrefix, "false").Inc()
}

func copyToStorage(storageClient *storage.Client, snapshotPath string, destURL *url.URL, ctx context.Context) error {
	if destURL.Scheme != "gs" {
		return fmt.Errorf("unsupported destination URL scheme: %s", destURL.Scheme)
	}
	storageWriter := storageClient.Bucket(destURL.Host).Object(destURL.Path).NewWriter(ctx)
	snapshotFile, err := os.Open(snapshotPath)
	if err != nil {
		return err
	}
	var snapshotFileContents []byte
	_, err = snapshotFile.Read(snapshotFileContents)
	if err != nil {
		return err
	}
	_, err = storageWriter.Write(snapshotFileContents)
	if err != nil {
		return err
	}
	_, err = storageWriter.Flush()
	if err != nil {
		return err
	}
	err = storageWriter.Close()
	if err != nil {
		return err
	}
	return nil
}

func TriggerHandlerFactory(args cli.Args, metrics *metrics.Metrics) func(w http.ResponseWriter, r *http.Request) {
	backend := logging.NewLogBackend(os.Stdout, "", 0)
	formatter := logging.NewBackendFormatter(backend, format)
	logging.SetBackend(formatter)
	return func (w http.ResponseWriter, r *http.Request) {
		// Only accept POST requests
		if (r.Method != "POST") {
			w.WriteHeader(http.StatusMethodNotAllowed)
			w.Write([]byte("{\"error\": \"Only POST is allowed\"}"))
			metrics.SnapshotCount.WithLabelValues("unknown", "false").Inc()
			return
		}
		startTime := time.Now()
		body, err := parseRequestBody(r.Body)
		if err != nil {
			handleError(w, err, body.PartitionPrefix, metrics, http.StatusBadRequest)
			return
		}
		metrics.SnapshotDuration.WithLabelValues(body.PartitionPrefix, "parse_request_body").Observe(time.Since(startTime).Abs().Seconds())
		vmClient, err := victoriametrics.NewClient(r.Context(), args.VictoriaLogsURL.String())
		log.Info("initialized vmClient")
		if err != nil {
			handleError(w, err, body.PartitionPrefix, metrics, http.StatusInternalServerError)
			return
		}
		destURL, err := url.Parse(body.DestinationURL)
		log.Infof("parsed destination url as %#v", destURL)
		if err != nil {
			handleError(w, err, body.PartitionPrefix, metrics, http.StatusBadRequest)
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
			w.Write([]byte("Snapshot created but no paths returned, nothing to copy"))
			metrics.SnapshotCount.WithLabelValues(body.PartitionPrefix, "true").Inc()
			return
		}
		if destURL.Scheme != "gs" {
			handleError(w, fmt.Errorf("unsupported destination URL scheme: %s, destUrl: %s", destURL.Scheme, destURL), body.PartitionPrefix, metrics, http.StatusBadRequest)
			return
		}
		storageClient, err := storage.NewClient(r.Context())
		if err != nil {
			handleError(w, err, body.PartitionPrefix, metrics, http.StatusInternalServerError)
			return
		}
		for _, snapshotPath := range snapshotPaths {
			log.Infof("Copying snapshot %s to storage with destination URL %s", snapshotPath, destURL.String())
			err = copyToStorage(storageClient, snapshotPath, destURL, r.Context())
			if err != nil {
				handleError(w, err, body.PartitionPrefix, metrics, http.StatusInternalServerError)
				return
			}
			log.Infof("Deleting snapshot %s", snapshotPath)
			err = vmClient.DeleteSnapshot(snapshotPath)
			if err != nil {
				handleError(w, err, body.PartitionPrefix, metrics, http.StatusInternalServerError)
				return
			}
		}

		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte("OK"))
		metrics.SnapshotCount.WithLabelValues(body.PartitionPrefix, "true").Inc()
	}
}
