package cli

import (
	"fmt"
	"log"
	"net/url"
	"os"

	"github.com/alexflint/go-arg"
)

var Version = "0.0.0-dev"

type Args struct {
	Host                string  `arg:"--host,env:VLBACKUP_HOST" help:"The host to bind the HTTP server to" default:":8080"`
	OpsHost             string  `arg:"--ops-host,env:VLBACKUP_OPS_HOST" help:"The host to bind the health/ready/metrics server to" default:":9090"`
	VictoriaLogsURL     url.URL `arg:"--victoria-logs-url,env:VLBACKUP_VICTORIA_LOGS_URL" help:"The VictoriaLogs URL" default:"http://127.0.0.1:9428"`
	VictoriaLogsAuthKey string  `arg:"--victoria-logs-auth-key,env:VLBACKUP_VICTORIA_LOGS_AUTH_KEY" help:"Optional auth key for victorialogs, use if VL -partitionManageAuthKey flag is set" default:""`
	DataPath            string  `arg:"--data-path,env:VLBACKUP_DATA_PATH" help:"Mount path of the VictoriaLogs data volume in this sidecar, must match VL -storageDataPath" default:"/data"`
	TransferAuthKey     string  `arg:"--transfer-auth-key,env:VLBACKUP_TRANSFER_AUTH_KEY" help:"Optional shared bearer token for inter-vlbackup transfer endpoints" default:""`
}

func (Args) Version() string {
	return fmt.Sprintf("vlbackup %s", Version)
}

func GetCliArgs() Args {
	var args Args
	p, err := arg.NewParser(arg.Config{}, &args)
	if err != nil {
		log.Fatalf("there was an error in the definition of the Go struct: %v", err)
	}

	err = p.Parse(os.Args[1:])
	switch {
	case err == arg.ErrHelp: // found "--help" on command line
		p.WriteHelp(os.Stdout)
		os.Exit(0)
	case err == arg.ErrVersion: // found "--version" on command line
		fmt.Println(args.Version())
		os.Exit(0)
	case err != nil:
		fmt.Printf("error: %v\n", err)
		p.WriteUsage(os.Stdout)
		os.Exit(1)
	}

	return args
}
