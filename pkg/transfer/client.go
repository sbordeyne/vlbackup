package transfer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	RECEIVE_PATH = "/v1/vlbackup/transfer/receive"
	ATTACH_PATH  = "/v1/vlbackup/transfer/attach"

	// maxAttempts and baseBackoff bound the retry of a single peer operation.
	// Backoff is exponential (baseBackoff << attempt): 0.5s, 1s, ...
	maxAttempts = 3
	baseBackoff = 500 * time.Millisecond
)

// backoff waits before the given (0-indexed) retry, returning early if ctx is
// cancelled so retries never outlive the request deadline.
func backoff(ctx context.Context, attempt int) error {
	t := time.NewTimer(baseBackoff << attempt)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// ErrConflict is returned when the target reports 409: the partition
// already exists on the target instance.
var ErrConflict = errors.New("partition already exists on target")

// SnapshotPathResolver maps a snapshot path as reported by VictoriaLogs
// to a path readable by this process. Identity by default: vlbackup is
// expected to mount the VL data volume at the same path as VL itself.
// Overridable for tests.
var SnapshotPathResolver = func(path string) string { return path }

// PeerClient talks to the vlbackup sidecar of another VictoriaLogs instance.
type PeerClient struct {
	baseURL url.URL
	authKey string
	Http    *http.Client
}

func NewPeerClient(baseURL, authKey string) (*PeerClient, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("invalid target URL %q: scheme and host are required", baseURL)
	}
	return &PeerClient{
		baseURL: *u,
		authKey: authKey,
		// No overall Timeout: transfers stream multi-GB bodies. Connection
		// setup is bounded, and ResponseHeaderTimeout bounds the target-side
		// extraction (it counts from the end of the request body write).
		// Cancellation comes from the request context.
		Http: &http.Client{
			Transport: &http.Transport{
				DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
				ResponseHeaderTimeout: 10 * time.Minute,
			},
		},
	}, nil
}

func (c *PeerClient) NewRequest(ctx context.Context, path, partition string, body io.Reader) (*http.Request, error) {
	u := c.baseURL.JoinPath(path)
	u.RawQuery = url.Values{"partition": {partition}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), body)
	if err != nil {
		return nil, err
	}
	if c.authKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.authKey)
	}
	return req, nil
}

func PeerError(op string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	return fmt.Errorf("%s failed with status %s: %s", op, resp.Status, string(body))
}

// SendPartition streams a tar.gz of snapshotDir to the target's receive
// endpoint. Returns the number of bytes the target reports having written.
// Returns ErrConflict if the partition already exists on the target. Transient
// failures (transport errors, 5xx, or an in-transit checksum mismatch) are
// retried with backoff; the snapshot dir is re-streamed on each attempt.
func (c *PeerClient) SendPartition(ctx context.Context, partition, snapshotDir string) (int64, error) {
	var lastErr error
	for attempt := range maxAttempts {
		if attempt > 0 {
			if err := backoff(ctx, attempt-1); err != nil {
				return 0, err
			}
		}
		n, retry, err := c.sendOnce(ctx, partition, snapshotDir)
		if err == nil {
			return n, nil
		}
		lastErr = err
		if !retry {
			return 0, err
		}
	}
	return 0, fmt.Errorf("transfer receive failed after %d attempts: %w", maxAttempts, lastErr)
}

// sendOnce performs a single send attempt. It reports whether the failure is
// worth retrying so SendPartition can distinguish transient faults from
// permanent ones (ErrConflict and other 4xx).
func (c *PeerClient) sendOnce(ctx context.Context, partition, snapshotDir string) (int64, bool, error) {
	pr, pw := io.Pipe()
	go func() {
		_, err := StreamDir(SnapshotPathResolver(snapshotDir), pw)
		pw.CloseWithError(err)
	}()
	req, err := c.NewRequest(ctx, RECEIVE_PATH, partition, pr)
	if err != nil {
		pr.CloseWithError(err)
		return 0, false, err
	}
	req.Header.Set("Content-Type", "application/gzip")
	resp, err := c.Http.Do(req)
	if err != nil {
		// Transport error (reset connection, timeout...): retryable.
		return 0, true, err
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusOK:
		var parsed struct {
			BytesWritten int64 `json:"bytes_written"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
			return 0, false, fmt.Errorf("failed to decode receive response: %w", err)
		}
		return parsed.BytesWritten, false, nil
	case http.StatusConflict:
		return 0, false, ErrConflict
	default:
		err := PeerError("transfer receive", resp)
		// 5xx is transient. A 400 checksum mismatch means the archive was
		// corrupted in transit (the target rejected it before it landed), which
		// is also worth retrying; other 4xx are permanent.
		retry := resp.StatusCode >= 500 ||
			(resp.StatusCode == http.StatusBadRequest && strings.Contains(err.Error(), "checksum mismatch"))
		return 0, retry, err
	}
}

// Attach asks the target to attach the partition to its VictoriaLogs instance.
// Transient failures (transport errors, 5xx) are retried with backoff; the
// target-side attach is idempotent, so a retry after a partial success is safe.
func (c *PeerClient) Attach(ctx context.Context, partition string) error {
	var lastErr error
	for attempt := range maxAttempts {
		if attempt > 0 {
			if err := backoff(ctx, attempt-1); err != nil {
				return err
			}
		}
		retry, err := c.attachOnce(ctx, partition)
		if err == nil {
			return nil
		}
		lastErr = err
		if !retry {
			return err
		}
	}
	return fmt.Errorf("transfer attach failed after %d attempts: %w", maxAttempts, lastErr)
}

func (c *PeerClient) attachOnce(ctx context.Context, partition string) (bool, error) {
	req, err := c.NewRequest(ctx, ATTACH_PATH, partition, nil)
	if err != nil {
		return false, err
	}
	resp, err := c.Http.Do(req)
	if err != nil {
		return true, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode >= 500, PeerError("transfer attach", resp)
	}
	return false, nil
}
