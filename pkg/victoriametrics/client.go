package victoriametrics

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

type Client struct {
	ctx context.Context
	url url.URL
}

const (
	CREATE_SNAPSHOT_PATH = "/internal/partition/snapshot/create"
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

	values.Add("partition_prefix", partitionPrefix)
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
