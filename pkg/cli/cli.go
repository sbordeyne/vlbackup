package cli

import (
	"fmt"
	"log"
	"net/url"
	"os"

	"github.com/alexflint/go-arg"
)

type Args struct {
	Host                 string        `arg:"env" help:"The host to bind the HTTP server to" default:":8080"`
	VictoriaLogsURL      url.URL        `arg:"env" help:"The VictoriaLogs URL" default:"http://127.0.0.1:9428"`
	VictoriaLogsAuthKey  string        `arg:"env" help:"Optional auth key for victorialogs, use if VL -partitionManageAuthKey flag is set" default:""`
}

func (Args) Version() string {
	return "vlbackup v1.0.0"
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
