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
	NoWait            bool   `arg:"--no-wait" help:"Return the job id immediately instead of polling until the migration finishes"`
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
	case 202:
		// The migration runs as a background job; poll it to completion (unless
		// --no-wait) and render its final per-day and recent outcome.
		status, err := waitForJob(ctx, c, resp.JSON202, m.NoWait)
		if err != nil || status == nil {
			return err
		}
		_ = emit(o, status, func() { printMigrate(status.Migrate) })
		if status.State == client.Failed {
			return jobFailedError("migrate", status)
		}
		return nil
	case 400:
		return apiError(resp.StatusCode(), resp.JSON400, resp.Body)
	case 409:
		return apiError(resp.StatusCode(), resp.JSON409, resp.Body)
	default:
		return fmt.Errorf("unexpected response starting migrate (HTTP %d): %s", resp.StatusCode(), string(resp.Body))
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
