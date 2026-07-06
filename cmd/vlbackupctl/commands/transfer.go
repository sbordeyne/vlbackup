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
	case 200:
		return emit(o, resp.JSON200, func() { printTransfer(resp.JSON200) })
	case 400:
		return apiError(resp.StatusCode(), resp.JSON400, resp.Body)
	default:
		// 500 still carries a TransferResponse listing per-day errors.
		if resp.JSON500 != nil {
			_ = emit(o, resp.JSON500, func() { printTransfer(resp.JSON500) })
		}
		return fmt.Errorf("transfer completed with errors (HTTP %d)", resp.StatusCode())
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
