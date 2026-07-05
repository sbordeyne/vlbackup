package main

import (
	"fmt"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/collectors/version"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sbordeyne/vlbackup/pkg/cli"
	"github.com/sbordeyne/vlbackup/pkg/http_ops"
	"github.com/sbordeyne/vlbackup/pkg/metrics"
	"github.com/sbordeyne/vlbackup/pkg/openapi"
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
	m := metrics.New(reg)

	// Main API: the schema-first OpenAPI handler.
	apiHandler := openapi.NewHandler(openapi.NewServer(args, m), args.TransferAuthKey)

	// Ops server: health/ready/metrics on a separate port, not part of the spec.
	ops := chi.NewRouter()
	ops.Use(middleware.Recoverer)
	ops.Get("/healthz", http_ops.HealthHandler)
	ops.Get("/readyz", http_ops.ReadyHandler)
	ops.Get("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{Registry: reg}).ServeHTTP)

	// Run both listeners in their own goroutines; the first error wins.
	errs := make(chan error, 2)
	go func() {
		fmt.Printf("Started API server on address %s\n", args.Host)
		errs <- http.ListenAndServe(args.Host, apiHandler)
	}()
	go func() {
		fmt.Printf("Started ops server on address %s\n", args.OpsHost)
		errs <- http.ListenAndServe(args.OpsHost, ops)
	}()
	log.Fatal(<-errs)
}
