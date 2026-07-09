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

	resp := s.transferSealedDays(ctx, peer, vmClient, days)
	if len(resp.Errors) > 0 {
		return TransferPartitions500JSONResponse(resp), nil
	}
	return TransferPartitions200JSONResponse(resp), nil
}

// transferSealedDays moves each sealed day's partition to the target vlbackup:
// snapshot the local partition, stream it to the target, delete the snapshot,
// detach it locally, and ask the target to attach it. Any hard error aborts the
// remaining days. It is shared by the transfer and migrate handlers.
func (s *Server) transferSealedDays(ctx context.Context, peer *transfer.PeerClient, vmClient victoriametrics.Client, days []string) TransferResponse {
	observeStage := func(partition, stage string, start time.Time) {
		s.metrics.TransferDuration.WithLabelValues(partition, stage).Observe(time.Since(start).Seconds())
	}

	resp := TransferResponse{Transferred: []string{}, Skipped: []string{}, Errors: []string{}}
	fail := func(day, stage string, err error) {
		log.Errorf("transfer of %s failed at %s: %v", day, stage, err)
		resp.Errors = append(resp.Errors, fmt.Sprintf("%s: %s: %v", day, stage, err))
		s.metrics.TransferCount.WithLabelValues(day, "error").Inc()
	}

	// Best-effort: clear snapshots orphaned by a previously killed run so a
	// resumed transfer starts from a clean slate. Non-fatal on failure.
	if err := vmClient.DeleteStaleSnapshots(s.args.VictoriaLogsAuthKey); err != nil {
		log.Warningf("failed to delete stale snapshots before transfer: %v", err)
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
			// Genuinely nothing on the source for this day: skip. (This is the
			// only skip — an already-present target partition is completed
			// below, not skipped, so an interrupted run resumes.)
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

		// Stream the snapshot to the target. ErrConflict (409) means a prior
		// run already delivered and sha1-verified this partition on the target;
		// fall through to attach + detach so the interrupted run completes,
		// instead of skipping and stranding the day.
		stageStart = time.Now()
		sent, err := peer.SendPartition(ctx, day, snapshotPath)
		alreadyOnTarget := errors.Is(err, transfer.ErrConflict)
		if err != nil && !alreadyOnTarget {
			_ = vmClient.DeleteSnapshot(snapshotPath, s.args.VictoriaLogsAuthKey)
			fail(day, "stream", err)
			break
		}
		if alreadyOnTarget {
			log.Infof("Partition %s already present on target, resuming attach/detach", day)
		} else {
			observeStage(day, "stream", stageStart)
			s.metrics.TransferBytes.WithLabelValues("sent").Add(float64(sent))
		}

		// Attach on the target BEFORE detaching the source. Until the target
		// confirms the attach, the source keeps its copy, so a crash anywhere up
		// to here leaves the day recoverable (intact on the source, at worst
		// unattached-but-verified on the target) and a re-run can finish it.
		stageStart = time.Now()
		if err := peer.Attach(ctx, day); err != nil {
			_ = vmClient.DeleteSnapshot(snapshotPath, s.args.VictoriaLogsAuthKey)
			fail(day, "attach", err)
			break
		}
		observeStage(day, "attach", stageStart)

		// Delete the snapshot before detaching: VictoriaLogs refuses to delete
		// snapshots of partitions that are no longer attached.
		if err := vmClient.DeleteSnapshot(snapshotPath, s.args.VictoriaLogsAuthKey); err != nil {
			fail(day, "snapshot_cleanup", err)
			break
		}

		// Detach the source last, only after the target attach is confirmed.
		stageStart = time.Now()
		if err := vmClient.DetachPartition(day, s.args.VictoriaLogsAuthKey); err != nil {
			fail(day, "detach", fmt.Errorf("%w — %s is attached on the target but still on the source; re-run to retry the detach", err, day))
			break
		}
		observeStage(day, "detach", stageStart)

		log.Infof("Transferred partition %s (%d bytes)", day, sent)
		resp.Transferred = append(resp.Transferred, day)
		s.metrics.TransferCount.WithLabelValues(day, "transferred").Inc()
	}

	return resp
}
