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
	Host                string  `arg:"env" help:"The host to bind the HTTP server to" default:":8080" env:"VLBACKUP_HOST"`
	VictoriaLogsURL     url.URL `arg:"env" help:"The VictoriaLogs URL" default:"http://127.0.0.1:9428" env:"VLBACKUP_VICTORIA_LOGS_URL"`
	VictoriaLogsAuthKey string  `arg:"env" help:"Optional auth key for victorialogs, use if VL -partitionManageAuthKey flag is set" default:"" env:"VLBACKUP_VICTORIA_LOGS_AUTH_KEY"`
	DataPath            string  `arg:"env" help:"Mount path of the VictoriaLogs data volume in this sidecar, must match VL -storageDataPath" default:"/data" env:"VLBACKUP_DATA_PATH"`
	TransferAuthKey     string  `arg:"env" help:"Optional shared bearer token for inter-vlbackup transfer endpoints" default:"" env:"VLBACKUP_TRANSFER_AUTH_KEY"`
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
