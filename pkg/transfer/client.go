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
	"time"
)

const (
	RECEIVE_PATH = "/api/v1/transfer/receive"
	ATTACH_PATH  = "/api/v1/transfer/attach"
)

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
	http    *http.Client
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
		http: &http.Client{
			Transport: &http.Transport{
				DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
				ResponseHeaderTimeout: 10 * time.Minute,
			},
		},
	}, nil
}

func (c *PeerClient) newRequest(ctx context.Context, path, partition string, body io.Reader) (*http.Request, error) {
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

func peerError(op string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	return fmt.Errorf("%s failed with status %s: %s", op, resp.Status, string(body))
}

// SendPartition streams a tar.gz of snapshotDir to the target's receive
// endpoint. Returns the number of bytes the target reports having written.
// Returns ErrConflict if the partition already exists on the target.
func (c *PeerClient) SendPartition(ctx context.Context, partition, snapshotDir string) (int64, error) {
	pr, pw := io.Pipe()
	go func() {
		pw.CloseWithError(StreamDir(SnapshotPathResolver(snapshotDir), pw))
	}()
	req, err := c.newRequest(ctx, RECEIVE_PATH, partition, pr)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/gzip")
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusOK:
		var parsed struct {
			BytesWritten int64 `json:"bytes_written"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
			return 0, fmt.Errorf("failed to decode receive response: %w", err)
		}
		return parsed.BytesWritten, nil
	case http.StatusConflict:
		return 0, ErrConflict
	default:
		return 0, peerError("transfer receive", resp)
	}
}

// Attach asks the target to attach the partition to its VictoriaLogs instance.
func (c *PeerClient) Attach(ctx context.Context, partition string) error {
	req, err := c.newRequest(ctx, ATTACH_PATH, partition, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return peerError("transfer attach", resp)
	}
	return nil
}
