package openapi

import (
	"context"
	"errors"

	"github.com/sbordeyne/vlbackup/pkg/victoriametrics"
)

// AttachSnapshot handles the final step on the target: attaching the received
// partition to the local VictoriaLogs instance.
func (s *Server) AttachSnapshot(ctx context.Context, request AttachSnapshotRequestObject) (AttachSnapshotResponseObject, error) {
	partition := request.Params.Partition
	if !partitionNameRe.MatchString(partition) {
		return AttachSnapshot400JSONResponse(errorResponse(errors.New("partition query param must be YYYYMMDD"), 400)), nil
	}
	vmClient, err := victoriametrics.NewClient(ctx, s.args.VictoriaLogsURL.String())
	if err != nil {
		return AttachSnapshot500JSONResponse(errorResponse(err, 500)), nil
	}
	if err := vmClient.AttachPartition(partition, s.args.VictoriaLogsAuthKey); err != nil {
		log.Errorf("failed to attach partition %s: %v", partition, err)
		return AttachSnapshot500JSONResponse(errorResponse(err, 500)), nil
	}
	log.Infof("Attached partition %s", partition)
	return AttachSnapshot200JSONResponse{Partition: partition}, nil
}
