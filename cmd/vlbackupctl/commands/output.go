package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/sbordeyne/vlbackup/pkg/client"
)

// jobPollInterval is how often the client polls a background job for progress.
const jobPollInterval = 2 * time.Second

// waitForJob polls a background transfer/migrate job until it reaches a terminal
// state and returns its final status; the caller renders it. With noWait it
// prints the job reference and returns (nil, nil) so the caller can exit early.
func waitForJob(ctx context.Context, c *client.ClientWithResponses, ref *client.JobRef, noWait bool) (*client.JobStatus, error) {
	if ref == nil {
		return nil, fmt.Errorf("server accepted the job but returned no job reference")
	}
	if noWait {
		fmt.Printf("job %s started; poll %s for status\n", ref.JobId, ref.StatusUrl)
		return nil, nil
	}
	for {
		resp, err := c.GetJobWithResponse(ctx, ref.JobId)
		if err != nil {
			return nil, fmt.Errorf("polling job %s: %w", ref.JobId, err)
		}
		if resp.StatusCode() != 200 {
			return nil, apiError(resp.StatusCode(), resp.JSON404, resp.Body)
		}
		if resp.JSON200.State != client.Running {
			return resp.JSON200, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(jobPollInterval):
		}
	}
}

// Options carries settings shared by every subcommand, populated from the
// global CLI flags.
type Options struct {
	// Output selects the rendering of a successful response: "text" or "json".
	Output string
}

// timeRange builds a client.TimeRange from the --from/--to flags. The API
// treats "to" as optional (defaulting to now), so it is only set when a value
// was provided.
func timeRange(from, to string) client.TimeRange {
	tr := client.TimeRange{From: from}
	if to != "" {
		tr.To = &to
	}
	return tr
}

// emit renders a successful response. In "json" mode it prints the raw payload
// as indented JSON; otherwise it invokes the text renderer.
func emit(o Options, payload any, text func()) error {
	if o.Output == "json" {
		b, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("encoding response as json: %w", err)
		}
		fmt.Println(string(b))
		return nil
	}
	text()
	return nil
}

// jobFailedError builds the error returned when a background job ends in the
// failed state, preferring the setup error when one is set (no per-day outcome
// was produced) over the generic per-day-errors message.
func jobFailedError(kind string, status *client.JobStatus) error {
	if status.Error != nil && *status.Error != "" {
		return fmt.Errorf("%s job %s failed: %s", kind, status.JobId, *status.Error)
	}
	return fmt.Errorf("%s job %s completed with errors", kind, status.JobId)
}

// apiError turns a non-2xx response carrying an ErrorResponse body into a Go
// error, falling back to the raw body when the structured error is absent.
func apiError(status int, errResp *client.ErrorResponse, body []byte) error {
	if errResp != nil && errResp.Error != nil {
		return fmt.Errorf("request failed (HTTP %d): %s", status, *errResp.Error)
	}
	if len(body) > 0 {
		return fmt.Errorf("request failed (HTTP %d): %s", status, string(body))
	}
	return fmt.Errorf("request failed with HTTP %d", status)
}
