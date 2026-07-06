package commands

import (
	"context"
	"fmt"

	"github.com/sbordeyne/vlbackup/pkg/client"
)

type SnapshotCmd struct {
	From    string `arg:"--from,required" help:"Start of the range, inclusive (time expression, e.g. now-7d/d)"`
	To      string `arg:"--to" help:"End of the range, inclusive (time expression, defaults to now)"`
	DestUrl string `arg:"--dest-url,required" help:"Object Storage destination URL, e.g. gs://bucket/prefix/ or s3://bucket/prefix/"`
}

func (s *SnapshotCmd) Run(ctx context.Context, c *client.ClientWithResponses, o Options) error {
	resp, err := c.TriggerSnapshotWithResponse(ctx, client.SnapshotRequest{
		DestinationUrl: s.DestUrl,
		Range:          timeRange(s.From, s.To),
	})
	if err != nil {
		return fmt.Errorf("calling snapshot: %w", err)
	}

	switch resp.StatusCode() {
	case 202:
		return emit(o, map[string]string{"status": "accepted"}, func() {
			fmt.Println("Snapshot request accepted.")
		})
	case 400:
		return apiError(resp.StatusCode(), resp.JSON400, resp.Body)
	default:
		return apiError(resp.StatusCode(), resp.JSON500, resp.Body)
	}
}
