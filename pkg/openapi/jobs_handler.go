package openapi

import (
	"context"
	"errors"
	"fmt"
)

// errActiveJob is returned (as a 409) when a transfer or migrate is requested
// while another is still running. Only one runs at a time — see JobStore.
var errActiveJob = errors.New("a transfer or migrate job is already running")

// jobRef builds the 202 body pointing at a started job's status URL.
func jobRef(job *Job) JobRef {
	return JobRef{
		JobId:     job.ID,
		StatusUrl: "/v1/vlbackup/jobs/" + job.ID,
	}
}

// recoverJob is deferred by every background job body so a panic frees the
// single-flight slot and surfaces as a failed job instead of a silent leak.
func (s *Server) recoverJob(jobID, kind string) {
	if r := recover(); r != nil {
		log.Errorf("%s job %s panicked: %v", kind, jobID, r)
		s.jobs.Fail(jobID, fmt.Errorf("%s job panicked: %v", kind, r))
	}
}

// GetJob returns the current state of a background transfer or migrate job.
func (s *Server) GetJob(_ context.Context, request GetJobRequestObject) (GetJobResponseObject, error) {
	status, ok := s.jobs.Status(string(request.JobId))
	if !ok {
		return GetJob404JSONResponse(errorResponse(fmt.Errorf("no job with id %q", request.JobId), 404)), nil
	}
	return GetJob200JSONResponse(status), nil
}
