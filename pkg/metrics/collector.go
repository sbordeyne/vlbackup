package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Metrics struct {
	SnapshotCount *prometheus.CounterVec
	SnapshotDuration *prometheus.HistogramVec
	TransferCount *prometheus.CounterVec
	TransferDuration *prometheus.HistogramVec
	TransferBytes *prometheus.CounterVec
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
		TransferCount: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "vlbackup_transfer_count",
				Help: "Number of partition transfers, by result (transferred, skipped, error)",
			},
			[]string{"partition", "result"},
		),
		TransferDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "vlbackup_transfer_duration_seconds",
				Help:    "Duration of partition transfer stages in seconds.",
				Buckets: prometheus.ExponentialBuckets(0.1, 2, 14), // ~0.1s to ~13min, streams can take minutes
			},
			[]string{"partition", "stage"},
		),
		TransferBytes: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "vlbackup_transfer_bytes_total",
				Help: "Total bytes transferred between vlbackup instances, by direction (sent, received)",
			},
			[]string{"direction"},
		),
	}

	// Register metrics
	reg.MustRegister(m.SnapshotDuration, m.SnapshotCount, m.TransferCount, m.TransferDuration, m.TransferBytes)
	return m
}

func Handler() http.Handler {
	return promhttp.Handler()
}
