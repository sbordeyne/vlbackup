package http_handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	"cloud.google.com/go/storage"
	"github.com/sbordeyne/vlbackup/pkg/cli"
	"github.com/sbordeyne/vlbackup/pkg/victoriametrics"
	"github.com/sbordeyne/vlbackup/pkg/metrics"
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
	err := decoder.Decode(&body)
	if err != nil {
		return parsed, err;
	}
	return parsed, nil
}

func TriggerHandlerFactory(args cli.Args, metrics *metrics.Metrics) func(w http.ResponseWriter, r *http.Request) {
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
			fmt.Fprintf(w, "{\"error\": \"%#v\"}", err)
			w.WriteHeader(http.StatusBadRequest)
			metrics.SnapshotCount.WithLabelValues(body.PartitionPrefix, "false").Inc()
			return
		}
		metrics.SnapshotDuration.WithLabelValues(body.PartitionPrefix, "parse_request_body").Observe(time.Since(startTime).Abs().Seconds())
		vmClient, err := victoriametrics.NewClient(r.Context(), args.VictoriaLogsURL.String())
		if err != nil {
			fmt.Fprintf(w, "{\"error\": \"%#v\"}", err)
			w.WriteHeader(http.StatusBadRequest)
			metrics.SnapshotCount.WithLabelValues(body.PartitionPrefix, "false").Inc()
			return
		}
		destURL, err := url.Parse(body.DestinationURL)
		if err != nil {
			fmt.Fprintf(w, "{\"error\": \"%#v\"}", err)
			w.WriteHeader(http.StatusBadRequest)
			metrics.SnapshotCount.WithLabelValues(body.PartitionPrefix, "false").Inc()
			return
		}
		snapshotPaths, err := vmClient.CreateSnapshot(body.PartitionPrefix, args.VictoriaLogsAuthKey)
		if err != nil {
			fmt.Fprintf(w, "{\"error\": \"%#v\"}", err)
			w.WriteHeader(http.StatusInternalServerError)
			metrics.SnapshotCount.WithLabelValues(body.PartitionPrefix, "false").Inc()
			return
		}
		if destURL.Scheme != "gs" {
			fmt.Fprintf(w, "{\"error\": \"Unsupported destination scheme: %s\"}", destURL.Scheme)
			w.WriteHeader(http.StatusBadRequest)
			metrics.SnapshotCount.WithLabelValues(body.PartitionPrefix, "false").Inc()
			return
		}
		storageClient, err := storage.NewClient(r.Context())
		if err != nil {
			fmt.Fprintf(w, "{\"error\": \"%#v\"}", err)
			w.WriteHeader(http.StatusInternalServerError)
			metrics.SnapshotCount.WithLabelValues(body.PartitionPrefix, "false").Inc()
			return
		}
		for _, snapshotPath := range snapshotPaths {
			storageWriter := storageClient.Bucket(destURL.Host).Object(destURL.Path).NewWriter(r.Context())
			snapshotFile, err := os.Open(snapshotPath)
			if err != nil {
				fmt.Fprintf(w, "{\"error\": \"%#v\"}", err)
				w.WriteHeader(http.StatusInternalServerError)
				metrics.SnapshotCount.WithLabelValues(body.PartitionPrefix, "false").Inc()
				return
			}
			var snapshotFileContents []byte
			_, err = snapshotFile.Read(snapshotFileContents)
			if err != nil {
				fmt.Fprintf(w, "{\"error\": \"%#v\"}", err)
				w.WriteHeader(http.StatusInternalServerError)
				metrics.SnapshotCount.WithLabelValues(body.PartitionPrefix, "false").Inc()
				return
			}
			_, err = storageWriter.Write(snapshotFileContents)
			if err != nil {
				fmt.Fprintf(w, "{\"error\": \"%#v\"}", err)
				w.WriteHeader(http.StatusInternalServerError)
				metrics.SnapshotCount.WithLabelValues(body.PartitionPrefix, "false").Inc()
				return
			}
			_, err = storageWriter.Flush()
			if err != nil {
				fmt.Fprintf(w, "{\"error\": \"%#v\"}", err)
				w.WriteHeader(http.StatusInternalServerError)
				metrics.SnapshotCount.WithLabelValues(body.PartitionPrefix, "false").Inc()
				return
			}
			err = storageWriter.Close()
			if err != nil {
				fmt.Fprintf(w, "{\"error\": \"%#v\"}", err)
				w.WriteHeader(http.StatusInternalServerError)
				metrics.SnapshotCount.WithLabelValues(body.PartitionPrefix, "false").Inc()
				return
			}
		}

		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte("OK"))
		metrics.SnapshotCount.WithLabelValues(body.PartitionPrefix, "true").Inc()
	}
}
