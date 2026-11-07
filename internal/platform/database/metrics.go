package database

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"ticker/db/queries"
)

type traceKey uint8

const (
	acquireKey traceKey = iota
	queryKey
	batchKey
)

type queryTiming struct {
	start time.Time
	query string
}

type metricsTracer struct {
	acquire *prometheus.HistogramVec
	query   *prometheus.HistogramVec
	batch   *prometheus.HistogramVec
}

func newMetricsTracer() *metricsTracer {
	buckets := []float64{.0001, .0005, .001, .0025, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5}
	return &metricsTracer{
		acquire: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "ticker_db_acquire_seconds", Help: "Pool acquisition duration, including queueing and connection creation; errors included.", Buckets: buckets,
		}, []string{"outcome"}),
		query: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "ticker_db_query_seconds", Help: "Client query duration through row consumption, excluding pool acquisition; includes network and database waits.", Buckets: buckets,
		}, []string{"query", "outcome"}),
		batch: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "ticker_db_batch_seconds", Help: "Client batch duration through result consumption, excluding pool acquisition.", Buckets: buckets,
		}, []string{"outcome"}),
	}
}

func (m *metricsTracer) TraceAcquireStart(ctx context.Context, _ *pgxpool.Pool, _ pgxpool.TraceAcquireStartData) context.Context {
	return context.WithValue(ctx, acquireKey, time.Now())
}

func (m *metricsTracer) TraceAcquireEnd(ctx context.Context, _ *pgxpool.Pool, data pgxpool.TraceAcquireEndData) {
	m.acquire.WithLabelValues(outcome(data.Err)).Observe(time.Since(ctx.Value(acquireKey).(time.Time)).Seconds())
}

func (m *metricsTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	// Fixed labels: never expose SQL, parameters, customer IDs, or credentials.
	name := "other"
	switch data.SQL {
	case queries.ListPublishedEvents:
		name = "catalog_list"
	case queries.GetPublishedEvent:
		name = "catalog_event"
	case queries.ListEventSeats:
		name = "catalog_seats"
	}
	return context.WithValue(ctx, queryKey, queryTiming{start: time.Now(), query: name})
}

func (m *metricsTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	timing := ctx.Value(queryKey).(queryTiming)
	m.query.WithLabelValues(timing.query, outcome(data.Err)).Observe(time.Since(timing.start).Seconds())
}

func (m *metricsTracer) TraceBatchStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceBatchStartData) context.Context {
	return context.WithValue(ctx, batchKey, time.Now())
}

// pgx reports individual batch results without individual start times. Measure
// the entire batch separately instead of attributing its time to each statement.
func (m *metricsTracer) TraceBatchQuery(context.Context, *pgx.Conn, pgx.TraceBatchQueryData) {}

func (m *metricsTracer) TraceBatchEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceBatchEndData) {
	m.batch.WithLabelValues(outcome(data.Err)).Observe(time.Since(ctx.Value(batchKey).(time.Time)).Seconds())
}

func outcome(err error) string {
	if err != nil {
		return "error"
	}
	return "success"
}

// MetricsHandler keeps collectors scoped to the pool rather than registering
// pool pointers globally. Scraping these statistics never acquires a connection.
func MetricsHandler(pool *pgxpool.Pool) http.Handler {
	registry := prometheus.NewRegistry()
	if tracer, ok := pool.Config().ConnConfig.Tracer.(*metricsTracer); ok {
		registry.MustRegister(tracer.acquire, tracer.query, tracer.batch)
	}
	for _, metric := range []struct {
		name, help string
		value      func() float64
	}{
		{"ticker_db_pool_acquired_connections", "Connections currently acquired.", func() float64 { return float64(pool.Stat().AcquiredConns()) }},
		{"ticker_db_pool_idle_connections", "Idle connections.", func() float64 { return float64(pool.Stat().IdleConns()) }},
		{"ticker_db_pool_max_connections", "Configured maximum connections.", func() float64 { return float64(pool.Stat().MaxConns()) }},
	} {
		registry.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: metric.name, Help: metric.help}, metric.value))
	}
	for _, metric := range []struct {
		name, help string
		value      func() float64
	}{
		{"ticker_db_pool_empty_acquires_total", "Successful acquires that waited because the pool was empty.", func() float64 { return float64(pool.Stat().EmptyAcquireCount()) }},
		{"ticker_db_pool_empty_wait_seconds_total", "Cumulative wait for successful acquires from an empty pool; excludes cancelled acquires.", func() float64 { return pool.Stat().EmptyAcquireWaitTime().Seconds() }},
		{"ticker_db_pool_cancelled_acquires_total", "Acquires cancelled by their context.", func() float64 { return float64(pool.Stat().CanceledAcquireCount()) }},
	} {
		registry.MustRegister(prometheus.NewCounterFunc(prometheus.CounterOpts{Name: metric.name, Help: metric.help}, metric.value))
	}
	// Preserve the scrape-handler metrics exposed by promhttp.Handler.
	return promhttp.InstrumentMetricHandler(prometheus.DefaultRegisterer,
		promhttp.HandlerFor(prometheus.Gatherers{prometheus.DefaultGatherer, registry}, promhttp.HandlerOpts{}))
}
