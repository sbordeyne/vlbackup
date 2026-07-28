package openapi

import (
	"errors"
	"testing"
)

func TestJobStoreSingleFlight(t *testing.T) {
	s := NewJobStore()

	first := s.Start(Transfer)
	if first == nil {
		t.Fatal("first Start returned nil, want a job")
	}
	if first.State != Running {
		t.Errorf("state = %s, want running", first.State)
	}
	if s.Start(Migrate) != nil {
		t.Error("second Start returned a job while one is active, want nil (409)")
	}

	// Finishing the active job frees the slot for the next one.
	s.CompleteTransfer(first.ID, TransferResponse{Transferred: []string{"20240101"}})
	second := s.Start(Migrate)
	if second == nil {
		t.Fatal("Start after completion returned nil, want a job")
	}
	if second.ID == first.ID {
		t.Error("second job reused the first job's id")
	}
}

func TestJobStoreStatusTransitions(t *testing.T) {
	t.Run("clean transfer succeeds", func(t *testing.T) {
		s := NewJobStore()
		job := s.Start(Transfer)
		s.CompleteTransfer(job.ID, TransferResponse{Transferred: []string{"20240101"}, Skipped: []string{}, Errors: []string{}})
		status, ok := s.Status(job.ID)
		if !ok {
			t.Fatal("Status ok = false, want true")
		}
		if status.State != Succeeded {
			t.Errorf("state = %s, want succeeded", status.State)
		}
		if status.FinishedAt == nil {
			t.Error("finished_at = nil, want set")
		}
		if status.Transfer == nil || len(status.Transfer.Transferred) != 1 {
			t.Errorf("transfer = %+v, want the completed outcome", status.Transfer)
		}
	})

	t.Run("transfer with per-day errors fails", func(t *testing.T) {
		s := NewJobStore()
		job := s.Start(Transfer)
		s.CompleteTransfer(job.ID, TransferResponse{Errors: []string{"20240101: stream: boom"}})
		status, _ := s.Status(job.ID)
		if status.State != Failed {
			t.Errorf("state = %s, want failed (per-day errors present)", status.State)
		}
	})

	t.Run("setup failure carries the error", func(t *testing.T) {
		s := NewJobStore()
		job := s.Start(Migrate)
		s.Fail(job.ID, errors.New("vl client unreachable"))
		status, _ := s.Status(job.ID)
		if status.State != Failed {
			t.Errorf("state = %s, want failed", status.State)
		}
		if status.Error == nil || *status.Error != "vl client unreachable" {
			t.Errorf("error = %v, want the setup error", status.Error)
		}
	})
}

func TestJobStoreUnknownID(t *testing.T) {
	s := NewJobStore()
	if _, ok := s.Status("deadbeef"); ok {
		t.Error("Status ok = true for unknown id, want false")
	}
}

func TestNewJobIDFormat(t *testing.T) {
	id := newJobID()
	if len(id) != 32 {
		t.Errorf("len(id) = %d, want 32", len(id))
	}
	if newJobID() == id {
		t.Error("two ids collided, want distinct random ids")
	}
}
