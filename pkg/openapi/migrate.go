package openapi

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/sbordeyne/vlbackup/pkg/transfer"
	"github.com/sbordeyne/vlbackup/pkg/victoriametrics"
)

// countingReader counts the bytes read through it, so the migrate handler can
// report how much JSONLine data it streamed to the target while still passing
// the source stream straight through to the ingest request body.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// MigratePartitions moves sealed partitions like transfer, then copies today's
// still-open data at the record level: it queries the local VictoriaLogs over
// LogsQL as streamed JSONLine and ingests it into the target's JSON stream API.
// Sealed days are moved (detached from the source); today's data is copied (the
// source is left intact) and ingestion is at-least-once — a re-run re-inserts
// today's rows, since VictoriaLogs does not deduplicate on ingest.
func (s *Server) MigratePartitions(ctx context.Context, request MigratePartitionsRequestObject) (MigratePartitionsResponseObject, error) {
	now := time.Now()
	from, to, err := ParseTimeRange(request.Body.Range, now)
	if err != nil {
		return MigratePartitions400JSONResponse(errorResponse(err, 400)), nil
	}
	days, err := transfer.DaysInRange(from, to, now)
	if err != nil {
		return MigratePartitions400JSONResponse(errorResponse(err, 400)), nil
	}
	peer, err := transfer.NewPeerClient(request.Body.TargetVlbackupUrl, s.args.TransferAuthKey)
	if err != nil {
		return MigratePartitions400JSONResponse(errorResponse(err, 400)), nil
	}
	insertClient, err := victoriametrics.NewClient(ctx, request.Body.TargetVlinsertUrl)
	if err != nil {
		return MigratePartitions400JSONResponse(errorResponse(fmt.Errorf("target_vlinsert_url: %w", err), 400)), nil
	}
	selectClient, err := victoriametrics.NewClient(ctx, request.Body.TargetVlselectUrl)
	if err != nil {
		return MigratePartitions400JSONResponse(errorResponse(fmt.Errorf("target_vlselect_url: %w", err), 400)), nil
	}
	sourceClient, err := victoriametrics.NewClient(ctx, s.args.VictoriaLogsURL.String())
	if err != nil {
		return MigratePartitions500JSONResponse{Transferred: []string{}, Skipped: []string{}, Errors: []string{err.Error()}}, nil
	}

	// Phase 1: sealed days move exactly like a transfer.
	sealed := s.transferSealedDays(ctx, peer, sourceClient, days)
	resp := MigrateResponse{
		Transferred: sealed.Transferred,
		Skipped:     sealed.Skipped,
		Errors:      sealed.Errors,
	}

	// Phase 2: copy today's still-open data as streamed JSONLine. The two
	// phases target different endpoints, so the recent copy is attempted even
	// when a sealed day failed.
	targetAuthKey := ""
	if request.Body.TargetVlAuthKey != nil {
		targetAuthKey = *request.Body.TargetVlAuthKey
	}
	resp.Recent = s.migrateRecent(sourceClient, insertClient, selectClient, now, targetAuthKey, &resp.Errors)

	if len(resp.Errors) > 0 {
		return MigratePartitions500JSONResponse(resp), nil
	}
	return MigratePartitions200JSONResponse(resp), nil
}

// migrateRecent exports today's (UTC) data from the source and ingests it into
// the target, then verifies row counts on both sides. Errors are appended to
// errs; the returned RecentMigration is always non-nil and holds whatever was
// established before any failure.
func (s *Server) migrateRecent(source, insert, sel victoriametrics.Client, now time.Time, targetAuthKey string, errs *[]string) *RecentMigration {
	today := transfer.TruncateUTC(now)
	query := "_time:>=" + today.Format(time.RFC3339)
	recent := &RecentMigration{Partition: today.Format("20060102")}

	fail := func(stage string, err error) {
		log.Errorf("migrate recent data failed at %s: %v", stage, err)
		*errs = append(*errs, fmt.Sprintf("recent: %s: %v", stage, err))
		s.metrics.TransferCount.WithLabelValues("recent", "error").Inc()
	}

	// Export -> ingest, streamed: the source response body is passed straight
	// through as the ingest request body, counting bytes on the way.
	stageStart := time.Now()
	stream, err := source.QueryStream(query, s.args.VictoriaLogsAuthKey)
	if err != nil {
		fail("export", err)
		return recent
	}
	counter := &countingReader{r: stream}
	ingestErr := insert.Ingest(counter, targetAuthKey)
	_ = stream.Close()
	recent.BytesIngested = counter.n
	s.metrics.TransferBytes.WithLabelValues("exported").Add(float64(counter.n))
	if ingestErr != nil {
		fail("ingest", ingestErr)
		return recent
	}
	s.metrics.TransferBytes.WithLabelValues("ingested").Add(float64(counter.n))
	s.metrics.TransferDuration.WithLabelValues("recent", "ingest").Observe(time.Since(stageStart).Seconds())

	// Verify: compare row counts on source and target for today.
	verifyStart := time.Now()
	srcCount, err := source.Count(query, s.args.VictoriaLogsAuthKey)
	if err != nil {
		fail("verify_source", err)
		return recent
	}
	tgtCount, err := sel.Count(query, targetAuthKey)
	if err != nil {
		fail("verify_target", err)
		return recent
	}
	recent.SourceCount = srcCount
	recent.TargetCount = tgtCount
	recent.Verified = tgtCount >= srcCount
	s.metrics.TransferDuration.WithLabelValues("recent", "verify").Observe(time.Since(verifyStart).Seconds())
	// A count mismatch is advisory, not a hard failure: ingestion is
	// at-least-once and the target may not have made freshly ingested rows
	// queryable yet. Surface it via recent.verified rather than a 500.
	if !recent.Verified {
		log.Warningf("Recent data for %s not verified: target has %d rows, source has %d", recent.Partition, tgtCount, srcCount)
	}
	log.Infof("Migrated recent data for %s (%d bytes, source %d rows, target %d rows)", recent.Partition, recent.BytesIngested, srcCount, tgtCount)
	s.metrics.TransferCount.WithLabelValues("recent", "migrated").Inc()
	return recent
}
