package openapi

import (
	"context"
	"net/http"
)

// restoreNotImplemented is a RestoreSnapshotResponseObject that answers 501.
// It lets the stub satisfy the strict interface without a spec-defined
// response type for "not implemented".
type restoreNotImplemented struct{}

func (restoreNotImplemented) VisitRestoreSnapshotResponse(w http.ResponseWriter) error {
	writeError(w, http.StatusNotImplemented, "restore not implemented")
	return nil
}

// RestoreSnapshot is not yet implemented. It downloads a partition snapshot
// from Object Storage, extracts it into the local data path and attaches it to
// VictoriaLogs — TODO: implement the restore flow.
func (s *Server) RestoreSnapshot(ctx context.Context, request RestoreSnapshotRequestObject) (RestoreSnapshotResponseObject, error) {
	return restoreNotImplemented{}, nil
}
