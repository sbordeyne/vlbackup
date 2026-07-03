package http_handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/sbordeyne/vlbackup/pkg/cli"
	"github.com/sbordeyne/vlbackup/pkg/metrics"
	"github.com/sbordeyne/vlbackup/pkg/transfer"
	"github.com/sbordeyne/vlbackup/pkg/victoriametrics"
)

type TransferRange struct {
	From string `json:"from"`         // RFC3339, required
	To   string `json:"to,omitempty"` // RFC3339, optional, defaults to today
}

type TransferRequestBody struct {
	TargetURL string        `json:"target_url"`
	Range     TransferRange `json:"range"`
}

type TransferResponse struct {
	Transferred []string `json:"transferred"`
	Skipped     []string `json:"skipped"`
	Errors      []string `json:"errors,omitempty"`
}

func decodeJSONBody(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}

func parseTransferRange(rng TransferRange) (from, to time.Time, err error) {
	if rng.From == "" {
		return from, to, fmt.Errorf("range.from is required")
	}
	from, err = time.Parse(time.RFC3339, rng.From)
	if err != nil {
		return from, to, fmt.Errorf("invalid range.from: %w", err)
	}
	if rng.To != "" {
		to, err = time.Parse(time.RFC3339, rng.To)
		if err != nil {
			return from, to, fmt.Errorf("invalid range.to: %w", err)
		}
	}
	return from, to, nil
}

// TransferHandlerFactory handles the source side of a transfer: for each
// sealed day in the requested range it snapshots the local partition,
// streams it to the target vlbackup, detaches it locally, cleans up the
// snapshot, and asks the target to attach it. HTTP responses from the
// target act as acknowledgements; any hard error aborts remaining days.
func TransferHandlerFactory(args cli.Args, m *metrics.Metrics) http.HandlerFunc {
	observeStage := func(partition, stage string, start time.Time) {
		m.TransferDuration.WithLabelValues(partition, stage).Observe(time.Since(start).Seconds())
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var body TransferRequestBody
		if err := decodeJSONBody(r, &body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		from, to, err := parseTransferRange(body.Range)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		days, err := transfer.DaysInRange(from, to, time.Now())
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		peer, err := transfer.NewPeerClient(body.TargetURL, args.TransferAuthKey)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		vmClient, err := victoriametrics.NewClient(r.Context(), args.VictoriaLogsURL.String())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		resp := TransferResponse{Transferred: []string{}, Skipped: []string{}}
		fail := func(day, stage string, err error) {
			log.Errorf("transfer of %s failed at %s: %v", day, stage, err)
			resp.Errors = append(resp.Errors, fmt.Sprintf("%s: %s: %v", day, stage, err))
			m.TransferCount.WithLabelValues(day, "error").Inc()
		}

		for _, day := range days {
			stageStart := time.Now()
			snapshotPaths, err := vmClient.CreateSnapshot(day, args.VictoriaLogsAuthKey)
			if err != nil {
				fail(day, "snapshot", err)
				break
			}
			observeStage(day, "snapshot", stageStart)
			if len(snapshotPaths) == 0 {
				log.Infof("No partition for day %s, skipping", day)
				resp.Skipped = append(resp.Skipped, day)
				m.TransferCount.WithLabelValues(day, "skipped").Inc()
				continue
			}
			if len(snapshotPaths) != 1 {
				for _, p := range snapshotPaths {
					_ = vmClient.DeleteSnapshot(p, args.VictoriaLogsAuthKey)
				}
				fail(day, "snapshot", fmt.Errorf("expected exactly 1 snapshot path for partition %s, got %d", day, len(snapshotPaths)))
				break
			}
			snapshotPath := snapshotPaths[0]

			stageStart = time.Now()
			sent, err := peer.SendPartition(r.Context(), day, snapshotPath)
			if errors.Is(err, transfer.ErrConflict) {
				log.Infof("Partition %s already exists on target, skipping", day)
				_ = vmClient.DeleteSnapshot(snapshotPath, args.VictoriaLogsAuthKey)
				resp.Skipped = append(resp.Skipped, day)
				m.TransferCount.WithLabelValues(day, "skipped").Inc()
				continue
			}
			if err != nil {
				_ = vmClient.DeleteSnapshot(snapshotPath, args.VictoriaLogsAuthKey)
				fail(day, "stream", err)
				break
			}
			observeStage(day, "stream", stageStart)
			m.TransferBytes.WithLabelValues("sent").Add(float64(sent))

			// Delete the snapshot before detaching: VictoriaLogs refuses to
			// delete snapshots of partitions that are no longer attached.
			if err := vmClient.DeleteSnapshot(snapshotPath, args.VictoriaLogsAuthKey); err != nil {
				fail(day, "snapshot_cleanup", err)
				break
			}
			stageStart = time.Now()
			if err := vmClient.DetachPartition(day, args.VictoriaLogsAuthKey); err != nil {
				fail(day, "detach", err)
				break
			}
			observeStage(day, "detach", stageStart)

			stageStart = time.Now()
			if err := peer.Attach(r.Context(), day); err != nil {
				// The partition is detached here and only exists unattached on
				// the target: recover by re-calling the target's attach endpoint.
				fail(day, "attach", fmt.Errorf("%w — data for %s is on the target but unattached, retry POST %s?partition=%s on the target vlbackup", err, day, transfer.ATTACH_PATH, day))
				break
			}
			observeStage(day, "attach", stageStart)

			log.Infof("Transferred partition %s to %s (%d bytes)", day, body.TargetURL, sent)
			resp.Transferred = append(resp.Transferred, day)
			m.TransferCount.WithLabelValues(day, "transferred").Inc()
		}

		status := http.StatusOK
		if len(resp.Errors) > 0 {
			status = http.StatusInternalServerError
		}
		writeJSON(w, status, resp)
	}
}
