package commands

import (
	"context"
	"fmt"

	"github.com/sbordeyne/vlbackup/pkg/client"
)

type TransferCmd struct {
	From      string `arg:"--from,required" help:"Start of the range, inclusive (time expression, e.g. now-7d/d)"`
	To        string `arg:"--to" help:"End of the range, inclusive (time expression, defaults to now)"`
	TargetUrl string `arg:"--target-url,required" help:"Base URL of the target vlbackup instance, e.g. http://target-vlbackup:8080"`
	NoWait    bool   `arg:"--no-wait" help:"Return the job id immediately instead of polling until the transfer finishes"`
}

func (t *TransferCmd) Run(ctx context.Context, c *client.ClientWithResponses, o Options) error {
	resp, err := c.TransferPartitionsWithResponse(ctx, client.TransferRequest{
		TargetUrl: t.TargetUrl,
		Range:     timeRange(t.From, t.To),
	})
	if err != nil {
		return fmt.Errorf("calling transfer: %w", err)
	}

	switch resp.StatusCode() {
	case 202:
		// The transfer runs as a background job; poll it to completion (unless
		// --no-wait) and render its final per-day outcome.
		status, err := waitForJob(ctx, c, resp.JSON202, t.NoWait)
		if err != nil || status == nil {
			return err
		}
		_ = emit(o, status, func() { printTransfer(status.Transfer) })
		if status.State == client.Failed {
			return jobFailedError("transfer", status)
		}
		return nil
	case 400:
		return apiError(resp.StatusCode(), resp.JSON400, resp.Body)
	case 409:
		return apiError(resp.StatusCode(), resp.JSON409, resp.Body)
	default:
		return fmt.Errorf("unexpected response starting transfer (HTTP %d): %s", resp.StatusCode(), string(resp.Body))
	}
}

func printTransfer(r *client.TransferResponse) {
	if r == nil {
		return
	}
	fmt.Printf("transferred: %v\n", r.Transferred)
	fmt.Printf("skipped:     %v\n", r.Skipped)
	fmt.Printf("errors:      %v\n", r.Errors)
}
