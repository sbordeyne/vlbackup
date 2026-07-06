package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/alexflint/go-arg"
	cmds "github.com/sbordeyne/vlbackup/cmd/vlbackupctl/commands"
	"github.com/sbordeyne/vlbackup/pkg/client"
)

// Version is stamped at release time via -ldflags.
var Version = "0.0.0-dev"

type cli struct {
	Snapshot *cmds.SnapshotCmd `arg:"subcommand:snapshot" help:"Snapshot a range of partitions to Object Storage"`
	Restore  *cmds.RestoreCmd  `arg:"subcommand:restore" help:"Restore a partition from Object Storage"`
	Transfer *cmds.TransferCmd `arg:"subcommand:transfer" help:"Transfer partitions to another vlbackup instance"`
	Migrate  *cmds.MigrateCmd  `arg:"subcommand:migrate" help:"Migrate partitions and recent data to another vlbackup instance"`

	Url     string        `arg:"--url,env:VLBACKUPCTL_URL" default:"http://127.0.0.1:8080" help:"Base URL of the vlbackup API"`
	Timeout time.Duration `arg:"--timeout,env:VLBACKUPCTL_TIMEOUT" default:"30m" help:"HTTP client timeout"`
	Output  string        `arg:"--output,-o,env:VLBACKUPCTL_OUTPUT" default:"text" help:"Output format: text or json"`
}

func (cli) Version() string { return "vlbackupctl " + Version }

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	var args cli
	parser := arg.MustParse(&args)
	if parser.Subcommand() == nil {
		parser.WriteHelp(os.Stdout)
		return fmt.Errorf("no subcommand provided")
	}

	if args.Output != "text" && args.Output != "json" {
		return fmt.Errorf("invalid --output %q: want text or json", args.Output)
	}

	c, err := client.NewClientWithResponses(args.Url,
		client.WithHTTPClient(&http.Client{Timeout: args.Timeout}))
	if err != nil {
		return fmt.Errorf("building client: %w", err)
	}

	ctx := context.Background()
	opts := cmds.Options{Output: args.Output}

	switch {
	case args.Snapshot != nil:
		return args.Snapshot.Run(ctx, c, opts)
	case args.Restore != nil:
		return args.Restore.Run(ctx, c, opts)
	case args.Transfer != nil:
		return args.Transfer.Run(ctx, c, opts)
	case args.Migrate != nil:
		return args.Migrate.Run(ctx, c, opts)
	default:
		return fmt.Errorf("unknown subcommand")
	}
}
