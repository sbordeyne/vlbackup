package openapi

import (
	"context"
	"errors"
	"io"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/sbordeyne/vlbackup/pkg/objstore"
	"github.com/sbordeyne/vlbackup/pkg/transfer"
	"github.com/sbordeyne/vlbackup/pkg/victoriametrics"
)

// PartitionFromSnapshotPath extracts the partition name (YYYYMMDD) from a
// VictoriaLogs snapshot path like <dataPath>/partitions/<YYYYMMDD>/snapshots/<id>,
// falling back to the path's base name if the layout is unexpected.
func PartitionFromSnapshotPath(p string) string {
	segments := strings.Split(filepath.ToSlash(p), "/")
	for i, segment := range segments {
		if segment == "partitions" && i+1 < len(segments) {
			return segments[i+1]
		}
	}
	return filepath.Base(p)
}

// SnapshotFail records a failed snapshot for the partition and returns the
// matching error response envelope.
func (s *Server) SnapshotFail(partition string, err error, status int) ErrorResponse {
	log.Errorf("snapshot of %s failed: %v", partition, err)
	s.metrics.SnapshotCount.WithLabelValues(partition, "false").Inc()
	return errorResponse(err, status)
}

// TriggerSnapshot creates a VictoriaLogs snapshot of the requested partition,
// streams it as a tar.gz to the destination Object Storage bucket, and deletes
// the local snapshot. A partition holding no data is an empty success (202).
func (s *Server) TriggerSnapshot(ctx context.Context, request TriggerSnapshotRequestObject) (TriggerSnapshotResponseObject, error) {
	startTime := time.Now()

	// partition_prefix is optional; default to yesterday (UTC).
	partition := time.Now().Add(-time.Hour * 24).Format("20060102")
	if request.Body.PartitionPrefix != nil && *request.Body.PartitionPrefix != "" {
		partition = *request.Body.PartitionPrefix
	}
	destURL := request.Body.DestinationUrl
	s.metrics.SnapshotDuration.WithLabelValues(partition, "parse_request_body").Observe(time.Since(startTime).Abs().Seconds())

	repo, keyPrefix, err := objstore.Open(ctx, destURL)
	if err != nil {
		status := 500
		if errors.Is(err, objstore.ErrUnsupportedScheme) {
			return TriggerSnapshot400JSONResponse(s.SnapshotFail(partition, err, 400)), nil
		}
		return TriggerSnapshot500JSONResponse(s.SnapshotFail(partition, err, status)), nil
	}
	defer func() { _ = repo.Close() }()
	log.Infof("opened storage repository for destination %s", destURL)

	vmClient, err := victoriametrics.NewClient(ctx, s.args.VictoriaLogsURL.String())
	if err != nil {
		return TriggerSnapshot500JSONResponse(s.SnapshotFail(partition, err, 500)), nil
	}
	log.Info("initialized vmClient")

	snapshotPaths, err := vmClient.CreateSnapshot(partition, s.args.VictoriaLogsAuthKey)
	if err != nil {
		return TriggerSnapshot500JSONResponse(s.SnapshotFail(partition, err, 500)), nil
	}
	log.Infof("Created snapshot, got paths %#v", snapshotPaths)

	if len(snapshotPaths) == 0 {
		// No data to copy: the spec folds this into a 202 acknowledgement.
		s.metrics.SnapshotCount.WithLabelValues(partition, "true").Inc()
		return TriggerSnapshot202Response{}, nil
	}

	for _, snapshotPath := range snapshotPaths {
		key := path.Join(keyPrefix, PartitionFromSnapshotPath(snapshotPath)+".tar.gz")
		log.Infof("Uploading snapshot %s to %s as %s", snapshotPath, destURL, key)
		pr, pw := io.Pipe()
		go func() {
			pw.CloseWithError(transfer.StreamDir(transfer.SnapshotPathResolver(snapshotPath), pw))
		}()
		if err := repo.Upload(ctx, key, pr); err != nil {
			pr.CloseWithError(err)
			return TriggerSnapshot500JSONResponse(s.SnapshotFail(partition, err, 500)), nil
		}
		log.Infof("Deleting snapshot %s", snapshotPath)
		if err := vmClient.DeleteSnapshot(snapshotPath, s.args.VictoriaLogsAuthKey); err != nil {
			return TriggerSnapshot500JSONResponse(s.SnapshotFail(partition, err, 500)), nil
		}
	}

	s.metrics.SnapshotCount.WithLabelValues(partition, "true").Inc()
	return TriggerSnapshot202Response{}, nil
}
