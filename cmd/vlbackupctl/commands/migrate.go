package commands

import (
	"context"
	"fmt"

	"github.com/sbordeyne/vlbackup/pkg/client"
)

type MigrateCmd struct {
	From              string `arg:"--from,required" help:"Start of the range, inclusive (time expression, e.g. now-7d/d)"`
	To                string `arg:"--to" help:"End of the range, inclusive (time expression, defaults to now)"`
	TargetVlbackupUrl string `arg:"--target-vlbackup-url,required" help:"Base URL of the target vlbackup instance (receive/attach)"`
	TargetVlinsertUrl string `arg:"--target-vlinsert-url,required" help:"Base URL of the target VictoriaLogs insert API"`
	TargetVlselectUrl string `arg:"--target-vlselect-url,required" help:"Base URL of the target VictoriaLogs select API"`
	TargetVlAuthKey   string `arg:"--target-vl-auth-key,env:VLBACKUPCTL_TARGET_VL_AUTH_KEY" help:"Optional auth key for the target VictoriaLogs insert/select APIs"`
}

func (m *MigrateCmd) Run(ctx context.Context, c *client.ClientWithResponses, o Options) error {
	req := client.MigrateRequest{
		TargetVlbackupUrl: m.TargetVlbackupUrl,
		TargetVlinsertUrl: m.TargetVlinsertUrl,
		TargetVlselectUrl: m.TargetVlselectUrl,
		Range:             timeRange(m.From, m.To),
	}
	if m.TargetVlAuthKey != "" {
		req.TargetVlAuthKey = &m.TargetVlAuthKey
	}

	resp, err := c.MigratePartitionsWithResponse(ctx, req)
	if err != nil {
		return fmt.Errorf("calling migrate: %w", err)
	}

	switch resp.StatusCode() {
	case 200:
		return emit(o, resp.JSON200, func() { printMigrate(resp.JSON200) })
	case 400:
		return apiError(resp.StatusCode(), resp.JSON400, resp.Body)
	default:
		// 500 still carries a MigrateResponse listing per-day and recent errors.
		if resp.JSON500 != nil {
			_ = emit(o, resp.JSON500, func() { printMigrate(resp.JSON500) })
		}
		return fmt.Errorf("migrate completed with errors (HTTP %d)", resp.StatusCode())
	}
}

func printMigrate(r *client.MigrateResponse) {
	if r == nil {
		return
	}
	fmt.Printf("transferred: %v\n", r.Transferred)
	fmt.Printf("skipped:     %v\n", r.Skipped)
	fmt.Printf("errors:      %v\n", r.Errors)
	if r.Recent != nil {
		fmt.Printf("recent:      partition=%s bytes_ingested=%d source_count=%d target_count=%d verified=%t\n",
			r.Recent.Partition, r.Recent.BytesIngested, r.Recent.SourceCount,
			r.Recent.TargetCount, r.Recent.Verified)
	}
}
