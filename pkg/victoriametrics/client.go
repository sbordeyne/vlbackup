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
	CREATE_SNAPSHOT_PATH = "/internal/partition/snapshot/create"
	DELETE_SNAPSHOT_PATH = "/internal/partition/snapshot/delete"
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

	values.Add("p", partitionPrefix)
	if (authKey != "") {
		values.Add("authKey", authKey)
	}
	fullUrl := c.url.JoinPath(CREATE_SNAPSHOT_PATH)
	fullUrl.RawQuery = values.Encode()
	response, err := http.DefaultClient.Get(fullUrl.String())
	if err != nil {
		return nil, nil
	}
	decoder := json.NewDecoder(response.Body)
	var snapshotPaths []string
	err = decoder.Decode(&snapshotPaths)
	if err != nil {
		return nil, nil
	}
	return snapshotPaths, nil
}

func (c *Client) DeleteSnapshot(snapshotPath string) error {
	values := url.Values{}
	values.Add("path", snapshotPath)
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
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to delete snapshot: %s", response.Status)
	}
	return nil
}
