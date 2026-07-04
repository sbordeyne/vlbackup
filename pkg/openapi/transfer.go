package openapi

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sbordeyne/vlbackup/pkg/timeexpr"
	"github.com/sbordeyne/vlbackup/pkg/transfer"
	"github.com/sbordeyne/vlbackup/pkg/victoriametrics"
)

// ParseTimeRange resolves the inclusive [from, to] window. Both bounds are
// relative-or-absolute time expressions (see timeexpr.Parse) evaluated against
// now; a missing `to` defaults to now. It validates presence and ordering.
func ParseTimeRange(rng TimeRange, now time.Time) (from, to time.Time, err error) {
	if strings.TrimSpace(rng.From) == "" {
		return from, to, errors.New("range.from is required")
	}
	if from, err = timeexpr.Parse(rng.From, now); err != nil {
		return from, to, fmt.Errorf("range.from: %w", err)
	}
	if rng.To != nil && strings.TrimSpace(*rng.To) != "" {
		if to, err = timeexpr.Parse(*rng.To, now); err != nil {
			return from, to, fmt.Errorf("range.to: %w", err)
		}
	} else {
		to = now
	}
	if from.After(to) {
		return from, to, errors.New("range.from must be before range.to")
	}
	return from, to, nil
}

// TransferPartitions handles the source side of a transfer: for each sealed day
// in the requested range it snapshots the local partition, streams it to the
// target vlbackup, detaches it locally, cleans up the snapshot, and asks the
// target to attach it. Any hard error aborts the remaining days.
func (s *Server) TransferPartitions(ctx context.Context, request TransferPartitionsRequestObject) (TransferPartitionsResponseObject, error) {
	observeStage := func(partition, stage string, start time.Time) {
		s.metrics.TransferDuration.WithLabelValues(partition, stage).Observe(time.Since(start).Seconds())
	}

	now := time.Now()
	from, to, err := ParseTimeRange(request.Body.Range, now)
	if err != nil {
		return TransferPartitions400JSONResponse(errorResponse(err, 400)), nil
	}
	days, err := transfer.DaysInRange(from, to, now)
	if err != nil {
		return TransferPartitions400JSONResponse(errorResponse(err, 400)), nil
	}
	peer, err := transfer.NewPeerClient(request.Body.TargetUrl, s.args.TransferAuthKey)
	if err != nil {
		return TransferPartitions400JSONResponse(errorResponse(err, 400)), nil
	}
	vmClient, err := victoriametrics.NewClient(ctx, s.args.VictoriaLogsURL.String())
	if err != nil {
		return TransferPartitions500JSONResponse{Transferred: []string{}, Skipped: []string{}, Errors: []string{err.Error()}}, nil
	}

	resp := TransferResponse{Transferred: []string{}, Skipped: []string{}, Errors: []string{}}
	fail := func(day, stage string, err error) {
		log.Errorf("transfer of %s failed at %s: %v", day, stage, err)
		resp.Errors = append(resp.Errors, fmt.Sprintf("%s: %s: %v", day, stage, err))
		s.metrics.TransferCount.WithLabelValues(day, "error").Inc()
	}

	for _, day := range days {
		stageStart := time.Now()
		snapshotPaths, err := vmClient.CreateSnapshot(day, s.args.VictoriaLogsAuthKey)
		if err != nil {
			fail(day, "snapshot", err)
			break
		}
		observeStage(day, "snapshot", stageStart)
		if len(snapshotPaths) == 0 {
			log.Infof("No partition for day %s, skipping", day)
			resp.Skipped = append(resp.Skipped, day)
			s.metrics.TransferCount.WithLabelValues(day, "skipped").Inc()
			continue
		}
		if len(snapshotPaths) != 1 {
			for _, p := range snapshotPaths {
				_ = vmClient.DeleteSnapshot(p, s.args.VictoriaLogsAuthKey)
			}
			fail(day, "snapshot", fmt.Errorf("expected exactly 1 snapshot path for partition %s, got %d", day, len(snapshotPaths)))
			break
		}
		snapshotPath := snapshotPaths[0]

		stageStart = time.Now()
		sent, err := peer.SendPartition(ctx, day, snapshotPath)
		if errors.Is(err, transfer.ErrConflict) {
			log.Infof("Partition %s already exists on target, skipping", day)
			_ = vmClient.DeleteSnapshot(snapshotPath, s.args.VictoriaLogsAuthKey)
			resp.Skipped = append(resp.Skipped, day)
			s.metrics.TransferCount.WithLabelValues(day, "skipped").Inc()
			continue
		}
		if err != nil {
			_ = vmClient.DeleteSnapshot(snapshotPath, s.args.VictoriaLogsAuthKey)
			fail(day, "stream", err)
			break
		}
		observeStage(day, "stream", stageStart)
		s.metrics.TransferBytes.WithLabelValues("sent").Add(float64(sent))

		// Delete the snapshot before detaching: VictoriaLogs refuses to delete
		// snapshots of partitions that are no longer attached.
		if err := vmClient.DeleteSnapshot(snapshotPath, s.args.VictoriaLogsAuthKey); err != nil {
			fail(day, "snapshot_cleanup", err)
			break
		}
		stageStart = time.Now()
		if err := vmClient.DetachPartition(day, s.args.VictoriaLogsAuthKey); err != nil {
			fail(day, "detach", err)
			break
		}
		observeStage(day, "detach", stageStart)

		stageStart = time.Now()
		if err := peer.Attach(ctx, day); err != nil {
			// The partition is detached here and only exists unattached on the
			// target: recover by re-calling the target's attach endpoint.
			fail(day, "attach", fmt.Errorf("%w — data for %s is on the target but unattached, retry POST %s?partition=%s on the target vlbackup", err, day, transfer.ATTACH_PATH, day))
			break
		}
		observeStage(day, "attach", stageStart)

		log.Infof("Transferred partition %s to %s (%d bytes)", day, request.Body.TargetUrl, sent)
		resp.Transferred = append(resp.Transferred, day)
		s.metrics.TransferCount.WithLabelValues(day, "transferred").Inc()
	}

	if len(resp.Errors) > 0 {
		return TransferPartitions500JSONResponse(resp), nil
	}
	return TransferPartitions200JSONResponse(resp), nil
}
