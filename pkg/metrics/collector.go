package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Metrics struct {
	SnapshotCount *prometheus.CounterVec
	SnapshotDuration *prometheus.HistogramVec
}

func New(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		SnapshotDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "vlbackup_snapshot_duration_seconds",
				Help:    "Duration of secret rotation handling in seconds.",
				Buckets: prometheus.DefBuckets, // customize if rotations are usually fast/slow
			},
			[]string{"snapshot", "stage"},
		),
		SnapshotCount: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "vlbackup_snapshot_count",
				Help: "Number of snapshots performed",
			},
			[]string{"snapshot", "success"},
		),
	}

	// Register metrics
	reg.MustRegister(m.SnapshotDuration, m.SnapshotCount)
	return m
}

func Handler() http.Handler {
	return promhttp.Handler()
}
