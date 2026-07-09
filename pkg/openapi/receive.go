package openapi

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/sbordeyne/vlbackup/pkg/transfer"
)

// ReceiveSnapshot handles the target side of a transfer: it receives a tar.gz
// stream of a partition snapshot and extracts it into
// <DataPath>/partitions/<partition>, ready to be attached.
func (s *Server) ReceiveSnapshot(ctx context.Context, request ReceiveSnapshotRequestObject) (ReceiveSnapshotResponseObject, error) {
	partition := request.Params.Partition
	if !partitionNameRe.MatchString(partition) {
		return ReceiveSnapshot400JSONResponse(errorResponse(errors.New("partition query param must be YYYYMMDD"), 400)), nil
	}

	partitionsDir := filepath.Join(s.args.DataPath, "partitions")
	finalDir := filepath.Join(partitionsDir, partition)
	if _, err := os.Stat(finalDir); err == nil {
		return ReceiveSnapshot409JSONResponse(errorResponse(errors.New("partition already exists"), 409)), nil
	}
	if err := os.MkdirAll(partitionsDir, 0o755); err != nil {
		return ReceiveSnapshot500JSONResponse(errorResponse(err, 500)), nil
	}
	// Extract into a hidden temp dir on the same filesystem, then rename:
	// VictoriaLogs never sees a half-written partition directory.
	tmpDir, err := os.MkdirTemp(partitionsDir, "."+partition+".tmp-")
	if err != nil {
		return ReceiveSnapshot500JSONResponse(errorResponse(err, 500)), nil
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	written, digest, err := transfer.ExtractDir(request.Body, tmpDir)
	if err != nil {
		log.Errorf("failed to extract partition %s: %v", partition, err)
		return ReceiveSnapshot400JSONResponse(errorResponse(err, 400)), nil
	}
	if err := os.Rename(tmpDir, finalDir); err != nil {
		// A concurrent transfer of the same partition may have won the race.
		if _, statErr := os.Stat(finalDir); statErr == nil {
			return ReceiveSnapshot409JSONResponse(errorResponse(errors.New("partition already exists"), 409)), nil
		}
		return ReceiveSnapshot500JSONResponse(errorResponse(err, 500)), nil
	}
	log.Infof("Received partition %s (%d bytes, sha1 %s verified)", partition, written, digest)
	s.metrics.TransferBytes.WithLabelValues("received").Add(float64(written))
	return ReceiveSnapshot200JSONResponse{Partition: partition, BytesWritten: written}, nil
}
