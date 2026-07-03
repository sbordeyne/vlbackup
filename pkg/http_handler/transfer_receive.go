package http_handler

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"

	"github.com/sbordeyne/vlbackup/pkg/cli"
	"github.com/sbordeyne/vlbackup/pkg/metrics"
	"github.com/sbordeyne/vlbackup/pkg/transfer"
	"github.com/sbordeyne/vlbackup/pkg/victoriametrics"
)

// Partition names are per-day: YYYYMMDD. Validating this also guarantees
// the name is safe to join into a filesystem path.
var partitionNameRe = regexp.MustCompile(`^\d{8}$`)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Errorf("failed to encode JSON response: %v", err)
	}
}

func partitionParam(w http.ResponseWriter, r *http.Request) (string, bool) {
	partition := r.URL.Query().Get("partition")
	if !partitionNameRe.MatchString(partition) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "partition query param must be YYYYMMDD"})
		return "", false
	}
	return partition, true
}

// TransferReceiveHandlerFactory handles the target side of a transfer:
// it receives a tar.gz stream of a partition snapshot and extracts it
// into <DataPath>/partitions/<partition>, ready to be attached.
func TransferReceiveHandlerFactory(args cli.Args, m *metrics.Metrics) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		partition, ok := partitionParam(w, r)
		if !ok {
			return
		}
		partitionsDir := filepath.Join(args.DataPath, "partitions")
		finalDir := filepath.Join(partitionsDir, partition)
		if _, err := os.Stat(finalDir); err == nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "partition already exists"})
			return
		}
		if err := os.MkdirAll(partitionsDir, 0o755); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		// Extract into a hidden temp dir on the same filesystem, then rename:
		// VictoriaLogs never sees a half-written partition directory.
		tmpDir, err := os.MkdirTemp(partitionsDir, "."+partition+".tmp-")
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		defer os.RemoveAll(tmpDir)
		written, err := transfer.ExtractDir(r.Body, tmpDir)
		if err != nil {
			log.Errorf("failed to extract partition %s: %v", partition, err)
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := os.Rename(tmpDir, finalDir); err != nil {
			// A concurrent transfer of the same partition may have won the race.
			if _, statErr := os.Stat(finalDir); statErr == nil {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "partition already exists"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		log.Infof("Received partition %s (%d bytes)", partition, written)
		m.TransferBytes.WithLabelValues("received").Add(float64(written))
		writeJSON(w, http.StatusOK, map[string]any{"partition": partition, "bytes_written": written})
	}
}

// TransferAttachHandlerFactory handles the final step on the target:
// attaching the received partition to the local VictoriaLogs instance.
func TransferAttachHandlerFactory(args cli.Args, m *metrics.Metrics) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		partition, ok := partitionParam(w, r)
		if !ok {
			return
		}
		vmClient, err := victoriametrics.NewClient(r.Context(), args.VictoriaLogsURL.String())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if err := vmClient.AttachPartition(partition, args.VictoriaLogsAuthKey); err != nil {
			log.Errorf("failed to attach partition %s: %v", partition, err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		log.Infof("Attached partition %s", partition)
		writeJSON(w, http.StatusOK, map[string]string{"partition": partition})
	}
}
