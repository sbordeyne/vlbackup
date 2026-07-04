package openapi

import (
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"

	"github.com/sbordeyne/vlbackup/pkg/objstore"
	"github.com/sbordeyne/vlbackup/pkg/transfer"
	"github.com/sbordeyne/vlbackup/pkg/victoriametrics"
)

// RestoreSnapshot downloads a partition snapshot (a tar.gz) from the source
// Object Storage bucket, extracts it into the local data path and attaches it
// to VictoriaLogs.
func (s *Server) RestoreSnapshot(ctx context.Context, request RestoreSnapshotRequestObject) (RestoreSnapshotResponseObject, error) {
	partition := request.Body.PartitionPrefix
	if !partitionNameRe.MatchString(partition) {
		return RestoreSnapshot400JSONResponse(errorResponse(errors.New("partition_prefix must be YYYYMMDD"), 400)), nil
	}

	// Refuse before spending a download if the partition is already present:
	// restoring over a live partition is the caller's mistake, not ours.
	partitionsDir := filepath.Join(s.args.DataPath, "partitions")
	finalDir := filepath.Join(partitionsDir, partition)
	if _, err := os.Stat(finalDir); err == nil {
		return RestoreSnapshot409JSONResponse(errorResponse(errors.New("partition already attached locally"), 409)), nil
	}

	sourceURL := request.Body.SourceUrl
	repo, keyPrefix, err := objstore.Open(ctx, sourceURL)
	if err != nil {
		if errors.Is(err, objstore.ErrUnsupportedScheme) {
			return RestoreSnapshot400JSONResponse(errorResponse(err, 400)), nil
		}
		return RestoreSnapshot500JSONResponse(errorResponse(err, 500)), nil
	}
	defer func() { _ = repo.Close() }()
	log.Infof("opened storage repository for source %s", sourceURL)

	key := path.Join(keyPrefix, partition+".tar.gz")
	body, err := repo.Download(ctx, key)
	if err != nil {
		if errors.Is(err, objstore.ErrNotFound) {
			return RestoreSnapshot404JSONResponse(errorResponse(err, 404)), nil
		}
		return RestoreSnapshot500JSONResponse(errorResponse(err, 500)), nil
	}
	defer func() { _ = body.Close() }()
	log.Infof("Downloading snapshot %s from %s", key, sourceURL)

	if err := os.MkdirAll(partitionsDir, 0o755); err != nil {
		return RestoreSnapshot500JSONResponse(errorResponse(err, 500)), nil
	}
	// Extract into a hidden temp dir on the same filesystem, then rename:
	// VictoriaLogs never sees a half-written partition directory.
	tmpDir, err := os.MkdirTemp(partitionsDir, "."+partition+".tmp-")
	if err != nil {
		return RestoreSnapshot500JSONResponse(errorResponse(err, 500)), nil
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	written, err := transfer.ExtractDir(body, tmpDir)
	if err != nil {
		log.Errorf("failed to extract partition %s: %v", partition, err)
		return RestoreSnapshot500JSONResponse(errorResponse(err, 500)), nil
	}
	if err := os.Rename(tmpDir, finalDir); err != nil {
		// A concurrent restore of the same partition may have won the race.
		if _, statErr := os.Stat(finalDir); statErr == nil {
			return RestoreSnapshot409JSONResponse(errorResponse(errors.New("partition already attached locally"), 409)), nil
		}
		return RestoreSnapshot500JSONResponse(errorResponse(err, 500)), nil
	}

	vmClient, err := victoriametrics.NewClient(ctx, s.args.VictoriaLogsURL.String())
	if err != nil {
		return RestoreSnapshot500JSONResponse(errorResponse(err, 500)), nil
	}
	if err := vmClient.AttachPartition(partition, s.args.VictoriaLogsAuthKey); err != nil {
		log.Errorf("failed to attach partition %s: %v", partition, err)
		return RestoreSnapshot500JSONResponse(errorResponse(err, 500)), nil
	}

	log.Infof("Restored partition %s (%d bytes)", partition, written)
	s.metrics.TransferBytes.WithLabelValues("restored").Add(float64(written))
	return RestoreSnapshot202JSONResponse{Partition: []string{partition}, BytesWritten: written}, nil
}
