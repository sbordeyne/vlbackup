package commands

import (
	"context"
	"fmt"

	"github.com/sbordeyne/vlbackup/pkg/client"
)

type RestoreCmd struct {
	PartitionPrefix string `arg:"--partition-prefix,required" help:"Partition to restore, formatted as YYYYMMDD"`
	SourceUrl       string `arg:"--source-url,required" help:"Object Storage source URL, e.g. gs://bucket/prefix/ or s3://bucket/prefix/"`
}

func (r *RestoreCmd) Run(ctx context.Context, c *client.ClientWithResponses, o Options) error {
	resp, err := c.RestoreSnapshotWithResponse(ctx, client.RestoreRequest{
		SourceUrl:       r.SourceUrl,
		PartitionPrefix: r.PartitionPrefix,
	})
	if err != nil {
		return fmt.Errorf("calling restore: %w", err)
	}

	switch resp.StatusCode() {
	case 202:
		return emit(o, resp.JSON202, func() {
			fmt.Printf("restored partitions: %v (%d bytes written)\n",
				resp.JSON202.Partition, resp.JSON202.BytesWritten)
		})
	case 400:
		return apiError(resp.StatusCode(), resp.JSON400, resp.Body)
	case 404:
		return apiError(resp.StatusCode(), resp.JSON404, resp.Body)
	case 409:
		return apiError(resp.StatusCode(), resp.JSON409, resp.Body)
	default:
		return apiError(resp.StatusCode(), resp.JSON500, resp.Body)
	}
}
