package main

import (
	"fmt"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/collectors/version"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sbordeyne/vlbackup/pkg/cli"
	"github.com/sbordeyne/vlbackup/pkg/http_handler"
	"github.com/sbordeyne/vlbackup/pkg/metrics"
)



func main() {
	args := cli.GetCliArgs()
	reg := prometheus.NewRegistry()
	// Add go runtime metrics and process collectors.
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		version.NewCollector("vlbackup"),
	)
	metrics := metrics.New(reg)

	// Expose /metrics HTTP endpoint using the created custom registry.
	mux := http.NewServeMux()
	mux.HandleFunc("/readyz", http_handler.ReadyHandler)
	mux.HandleFunc("/healthz", http_handler.HealthHandler)
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{Registry: reg}))
	mux.HandleFunc("/snapshot", http_handler.TriggerHandlerFactory(args, metrics))
	fmt.Printf("Started server on address %s", args.Host)
	http.ListenAndServe(args.Host, mux)
}
