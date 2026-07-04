package main

import (
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
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

	r := chi.NewRouter()
	r.Use(middleware.Recoverer)

	r.Get("/readyz", http_handler.ReadyHandler)
	r.Get("/healthz", http_handler.HealthHandler)
	// Expose /metrics HTTP endpoint using the created custom registry.
	r.Get("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{Registry: reg}).ServeHTTP)
	r.Post("/snapshot", http_handler.TriggerHandlerFactory(args, metrics))
	r.Route("/api/v1", func(r chi.Router) {
		r.Post("/transfer", http_handler.TransferHandlerFactory(args, metrics))
		r.Group(func(r chi.Router) {
			r.Use(http_handler.BearerAuth(args.TransferAuthKey))
			r.Post("/transfer/receive", http_handler.TransferReceiveHandlerFactory(args, metrics))
			r.Post("/transfer/attach", http_handler.TransferAttachHandlerFactory(args, metrics))
		})
	})

	fmt.Printf("Started server on address %s", args.Host)
	http.ListenAndServe(args.Host, r)
}
