package victoriametrics

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
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
)

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
	defer response.Body.Close()
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
	defer response.Body.Close()
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
	defer response.Body.Close()
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
	defer response.Body.Close()
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
	defer response.Body.Close()
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
	defer response.Body.Close()
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
	defer response.Body.Close()
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
