package httpapi

import (
	"net/http"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var durationBuckets = []float64{5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000}

// Metrics holds all Prometheus instruments.
type Metrics struct {
	IngestDuration   prometheus.Histogram
	RetrieveDuration *prometheus.HistogramVec
	EmbedderFailures prometheus.Counter
	PendingVectors   prometheus.Gauge
	registry         *prometheus.Registry
}

// NewMetrics creates and registers all metrics with a fresh private registry.
func NewMetrics() *Metrics {
	reg := prometheus.NewRegistry()

	m := &Metrics{
		IngestDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "engram_ingest_duration_ms",
			Help:    "Duration of Store() calls in milliseconds.",
			Buckets: durationBuckets,
		}),
		RetrieveDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "engram_retrieve_duration_ms",
			Help:    "Duration of Retrieve() calls in milliseconds.",
			Buckets: durationBuckets,
		}, []string{"rerank"}),
		EmbedderFailures: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "engram_embedder_failures_total",
			Help: "Total number of embedder failures.",
		}),
		PendingVectors: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "engram_pending_vectors",
			Help: "Current count of rows in the pending_vectors table.",
		}),
		registry: reg,
	}

	reg.MustRegister(
		m.IngestDuration,
		m.RetrieveDuration,
		m.EmbedderFailures,
		m.PendingVectors,
	)

	return m
}

// Handler returns the HTTP handler for the /metrics endpoint.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// RecordIngest records the duration of a Store() call in milliseconds.
func (m *Metrics) RecordIngest(durationMs float64) {
	m.IngestDuration.Observe(durationMs)
}

// RecordRetrieve records the duration of a Retrieve() call in milliseconds,
// labelled by whether reranking was applied.
func (m *Metrics) RecordRetrieve(durationMs float64, rerank bool) {
	m.RetrieveDuration.With(prometheus.Labels{
		"rerank": strconv.FormatBool(rerank),
	}).Observe(durationMs)
}

// RecordEmbedderFailure increments the embedder failure counter.
func (m *Metrics) RecordEmbedderFailure() {
	m.EmbedderFailures.Inc()
}

// SetPendingVectors sets the pending_vectors gauge to n.
func (m *Metrics) SetPendingVectors(n float64) {
	m.PendingVectors.Set(n)
}
