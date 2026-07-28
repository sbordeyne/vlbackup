package openapi

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// Job is the in-memory record of one async transfer or migrate run. Transfer
// and Migrate hold the typed per-day outcome once the job reaches a terminal
// state; only the one matching Kind is ever populated.
type Job struct {
	ID         string
	Kind       JobStatusKind
	State      JobStatusState
	StartedAt  time.Time
	FinishedAt *time.Time
	Transfer   *TransferResponse
	Migrate    *MigrateResponse
	Err        string
}

// JobStore holds async jobs in memory. Because transfer and migrate both create
// VictoriaLogs snapshots and clear stale snapshots globally (DeleteStaleSnapshots
// wipes every stale snapshot, not just this run's), at most one job may be
// active at a time; Start enforces that single-flight invariant. Jobs are not
// persisted: they are lost on restart, which is acceptable because transfers are
// idempotent and re-runnable.
type JobStore struct {
	mu     sync.Mutex
	jobs   map[string]*Job
	active bool
	now    func() time.Time // overridable in tests
}

// NewJobStore returns an empty JobStore.
func NewJobStore() *JobStore {
	return &JobStore{jobs: make(map[string]*Job), now: time.Now}
}

// Start registers a new running job of the given kind, returning it. It returns
// nil if a job is already active: the caller should answer 409. The returned
// job MUST eventually be finished (via CompleteTransfer/CompleteMigrate/Fail),
// or the single-flight slot stays occupied for the process lifetime.
func (s *JobStore) Start(kind JobStatusKind) *Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active {
		return nil
	}
	job := &Job{
		ID:        newJobID(),
		Kind:      kind,
		State:     Running,
		StartedAt: s.now(),
	}
	s.jobs[job.ID] = job
	s.active = true
	return job
}

// Status returns the current state of the job as the API type, or ok=false if
// no job has that id.
func (s *JobStore) Status(id string) (JobStatus, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[id]
	if !ok {
		return JobStatus{}, false
	}
	status := JobStatus{
		JobId:      job.ID,
		Kind:       job.Kind,
		State:      job.State,
		StartedAt:  job.StartedAt,
		FinishedAt: job.FinishedAt,
		Transfer:   job.Transfer,
		Migrate:    job.Migrate,
	}
	if job.Err != "" {
		status.Error = &job.Err
	}
	return status, true
}

// CompleteTransfer records the outcome of a finished transfer job. Per-day
// errors mark the job failed; an otherwise clean run succeeds.
func (s *JobStore) CompleteTransfer(id string, resp TransferResponse) {
	r := resp
	s.finish(id, terminalState(len(resp.Errors)), func(j *Job) { j.Transfer = &r })
}

// CompleteMigrate records the outcome of a finished migrate job. Per-day or
// recent-phase errors mark the job failed; an otherwise clean run succeeds.
func (s *JobStore) CompleteMigrate(id string, resp MigrateResponse) {
	r := resp
	s.finish(id, terminalState(len(resp.Errors)), func(j *Job) { j.Migrate = &r })
}

// Fail marks the job failed with a setup error that occurred before any per-day
// result could be produced (e.g. the source VL client could not be created, or
// the run panicked).
func (s *JobStore) Fail(id string, err error) {
	s.finish(id, Failed, func(j *Job) { j.Err = err.Error() })
}

// finish records the terminal state of a job and frees the single-flight slot.
func (s *JobStore) finish(id string, state JobStatusState, mutate func(*Job)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[id]
	if !ok {
		return
	}
	fin := s.now()
	job.State = state
	job.FinishedAt = &fin
	mutate(job)
	s.active = false
}

// terminalState maps a per-day error count to the terminal job state.
func terminalState(nErrors int) JobStatusState {
	if nErrors > 0 {
		return Failed
	}
	return Succeeded
}

// newJobID returns a random 32-hex-character job id, matching the JobRef schema.
func newJobID() string {
	var b [16]byte
	_, _ = rand.Read(b[:]) // crypto/rand.Read never returns an error on supported platforms
	return hex.EncodeToString(b[:])
}
