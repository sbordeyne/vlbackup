package victoriametrics

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	ctx context.Context
	url url.URL
}

const (
	CREATE_SNAPSHOT_PATH       = "/internal/partition/snapshot/create"
	DELETE_SNAPSHOT_PATH       = "/internal/partition/snapshot/delete"
	DELETE_STALE_SNAPSHOT_PATH = "/internal/partition/snapshot/delete_stale"
	DETACH_PARTITION_PATH      = "/internal/partition/detach"
	ATTACH_PARTITION_PATH      = "/internal/partition/attach"
	LIST_PARTITIONS_PATH       = "/internal/partition/list"
	LIST_SNAPSHOTS_PATH        = "/internal/partition/snapshot/list"
	QUERY_PATH                 = "/select/logsql/query"
	INSERT_JSONLINE_PATH       = "/insert/jsonline"
)

// streamingClient is used for LogsQL query/ingest, which can move large,
// slow bodies. Like transfer.PeerClient it has no overall timeout: the dial
// is bounded, ResponseHeaderTimeout bounds the wait for the first byte, and
// cancellation comes from the request context.
var streamingClient = &http.Client{
	Transport: &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		ResponseHeaderTimeout: 10 * time.Minute,
	},
}

func NewClient(ctx context.Context, baseUrl string) (Client, error) {
	parsedUrl, err := url.Parse(baseUrl)
	if err != nil {
		return Client{}, err
	}
	return Client{
		ctx: ctx,
		url: *parsedUrl,
	}, nil
}

func (c *Client) CreateSnapshot(partitionPrefix, authKey string) ([]string, error) {
	values := url.Values{}

	if partitionPrefix != "" {
		values.Add("partition_prefix", partitionPrefix)
	}
	if authKey != "" {
		values.Add("authKey", authKey)
	}
	fullUrl := c.url.JoinPath(CREATE_SNAPSHOT_PATH)
	fullUrl.RawQuery = values.Encode()
	response, err := http.DefaultClient.Get(fullUrl.String())
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to create snapshot: %s", response.Status)
	}
	decoder := json.NewDecoder(response.Body)
	var snapshotPaths []string
	err = decoder.Decode(&snapshotPaths)
	if err != nil {
		return nil, err
	}
	return snapshotPaths, nil
}

func (c *Client) DeleteSnapshot(snapshotPath, authKey string) error {
	values := url.Values{}
	values.Add("path", snapshotPath)
	if authKey != "" {
		values.Add("authKey", authKey)
	}
	fullUrl := c.url.JoinPath(DELETE_SNAPSHOT_PATH)
	fullUrl.RawQuery = values.Encode()
	request, err := http.NewRequestWithContext(c.ctx, "DELETE", fullUrl.String(), nil)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("failed to delete snapshot: %s", response.Status)
	}
	return nil
}

func (c *Client) DeleteStaleSnapshots(authKey string) error {
	values := url.Values{}
	if authKey != "" {
		values.Add("authKey", authKey)
	}
	fullUrl := c.url.JoinPath(DELETE_STALE_SNAPSHOT_PATH)
	fullUrl.RawQuery = values.Encode()
	request, err := http.NewRequestWithContext(c.ctx, "DELETE", fullUrl.String(), nil)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("failed to delete stale snapshots: %s", response.Status)
	}
	return nil
}

func (c *Client) DetachPartition(partitionName, authKey string) error {
	values := url.Values{}
	values.Add("name", partitionName)
	if authKey != "" {
		values.Add("authKey", authKey)
	}
	fullUrl := c.url.JoinPath(DETACH_PARTITION_PATH)
	fullUrl.RawQuery = values.Encode()
	request, err := http.NewRequestWithContext(c.ctx, "POST", fullUrl.String(), nil)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to detach partition: %s", response.Status)
	}
	return nil
}

func (c *Client) AttachPartition(partitionName, authKey string) error {
	values := url.Values{}
	values.Add("name", partitionName)
	if authKey != "" {
		values.Add("authKey", authKey)
	}
	fullUrl := c.url.JoinPath(ATTACH_PARTITION_PATH)
	fullUrl.RawQuery = values.Encode()
	request, err := http.NewRequestWithContext(c.ctx, "POST", fullUrl.String(), nil)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to attach partition: %s", response.Status)
	}
	return nil
}
func (c *Client) ListPartitions(authKey string) ([]string, error) {
	values := url.Values{}
	if authKey != "" {
		values.Add("authKey", authKey)
	}
	fullUrl := c.url.JoinPath(LIST_PARTITIONS_PATH)
	fullUrl.RawQuery = values.Encode()
	response, err := http.DefaultClient.Get(fullUrl.String())
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to list partitions: %s", response.Status)
	}
	var partitions []string
	decoder := json.NewDecoder(response.Body)
	err = decoder.Decode(&partitions)
	if err != nil {
		return nil, err
	}
	return partitions, nil
}

func (c *Client) ListSnapshots(authKey string) ([]string, error) {
	values := url.Values{}
	if authKey != "" {
		values.Add("authKey", authKey)
	}
	fullUrl := c.url.JoinPath(LIST_SNAPSHOTS_PATH)
	fullUrl.RawQuery = values.Encode()
	response, err := http.DefaultClient.Get(fullUrl.String())
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to list snapshots: %s", response.Status)
	}
	var snapshots []string
	decoder := json.NewDecoder(response.Body)
	err = decoder.Decode(&snapshots)
	if err != nil {
		return nil, err
	}
	return snapshots, nil
}

// queryRequest builds a POST to the LogsQL query endpoint with the query (and
// optional authKey) form-encoded, matching `curl .../select/logsql/query -d query=...`.
func (c *Client) queryRequest(query, authKey string) (*http.Request, error) {
	values := url.Values{"query": {query}}
	if authKey != "" {
		values.Add("authKey", authKey)
	}
	fullUrl := c.url.JoinPath(QUERY_PATH)
	req, err := http.NewRequestWithContext(c.ctx, http.MethodPost, fullUrl.String(), strings.NewReader(values.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req, nil
}

// QueryStream runs a LogsQL query and returns the streaming JSONLine response
// body. The caller owns the returned ReadCloser and must Close it.
func (c *Client) QueryStream(query, authKey string) (io.ReadCloser, error) {
	req, err := c.queryRequest(query, authKey)
	if err != nil {
		return nil, err
	}
	response, err := streamingClient.Do(req)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
		_ = response.Body.Close()
		return nil, fmt.Errorf("failed to query logs: %s: %s", response.Status, string(body))
	}
	return response.Body, nil
}

// Ingest streams a JSONLine (newline-delimited JSON) body into the JSON stream
// ingestion API. It does not buffer the body, so it can move arbitrarily large
// exports.
func (c *Client) Ingest(body io.Reader, authKey string) error {
	values := url.Values{}
	if authKey != "" {
		values.Add("authKey", authKey)
	}
	fullUrl := c.url.JoinPath(INSERT_JSONLINE_PATH)
	fullUrl.RawQuery = values.Encode()
	req, err := http.NewRequestWithContext(c.ctx, http.MethodPost, fullUrl.String(), body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-ndjson")
	response, err := streamingClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusNoContent {
		resp, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
		return fmt.Errorf("failed to ingest logs: %s: %s", response.Status, string(resp))
	}
	return nil
}

// Count returns the number of log rows matching query, by wrapping it in a
// `| stats count() rows` pipe. VictoriaLogs returns the count as a single
// JSONLine object whose `rows` field is a decimal string.
func (c *Client) Count(query, authKey string) (int64, error) {
	req, err := c.queryRequest(query+" | stats count() rows", authKey)
	if err != nil {
		return 0, err
	}
	response, err := streamingClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
		return 0, fmt.Errorf("failed to count logs: %s: %s", response.Status, string(body))
	}
	// An empty result set yields no lines; treat that as zero.
	var result struct {
		Rows string `json:"rows"`
	}
	decoder := json.NewDecoder(response.Body)
	if err := decoder.Decode(&result); err != nil {
		if err == io.EOF {
			return 0, nil
		}
		return 0, err
	}
	if result.Rows == "" {
		return 0, nil
	}
	return strconv.ParseInt(result.Rows, 10, 64)
}
