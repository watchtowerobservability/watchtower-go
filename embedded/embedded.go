// Package wtembedded is the shared model for embedded and in-memory stores (no server): a client
// span per operation (OTel db semconv) and wt.embedded.* health metrics read from the store's own
// stats. The per-store packages (module datastores: bbolt, badger, pebble, leveldb, buntdb, ristretto) build on it;
// use it directly for any store they do not cover.
package wtembedded

import (
	"context"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

const scope = "github.com/watchtowerobservability/watchtower-go/embedded"

// Op describes one store operation. Keys and values are never recorded.
type Op struct {
	System     string // db.system.name: "bbolt", "badger", "sqlite", ...
	Namespace  string // db.namespace: file path or logical db name
	Operation  string // db.operation.name: "view", "update", "get", ...
	Collection string // db.collection.name: bucket, table, column family; optional
}

// Span runs fn inside a client span for op; an error marks the span and is returned unchanged.
func Span(ctx context.Context, op Op, fn func(context.Context) error) error {
	name := op.Operation
	if op.Collection != "" {
		name += " " + op.Collection
	}
	attrs := []attribute.KeyValue{
		attribute.String("db.system.name", op.System),
		attribute.String("db.operation.name", op.Operation),
	}
	if op.Namespace != "" {
		attrs = append(attrs, attribute.String("db.namespace", op.Namespace))
	}
	if op.Collection != "" {
		attrs = append(attrs, attribute.String("db.collection.name", op.Collection))
	}
	ctx, span := otel.Tracer(scope).Start(ctx, name, trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(attrs...))
	defer span.End()
	err := fn(ctx)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		span.SetAttributes(attribute.String("error.type", errType(err)))
	}
	return err
}

func errType(err error) string {
	if t, ok := err.(interface{ Type() string }); ok {
		return t.Type()
	}
	return "_OTHER"
}

// Recorder takes one reading per metric per collection. Names outside the wt.embedded.* set below
// are ignored. Counters take the cumulative total since the store opened.
type Recorder interface {
	Gauge(name string, v int64)
	Counter(name string, v int64)
}

// Gauges (current value) and counters (cumulative) the backend understands.
var (
	GaugeNames = map[string]string{
		"wt.embedded.file.size":          "By",
		"wt.embedded.wal.size":           "By",
		"wt.embedded.map.size":           "By",
		"wt.embedded.map.used":           "By",
		"wt.embedded.memory.used":        "By",
		"wt.embedded.cache.size":         "By",
		"wt.embedded.keys":               "{key}",
		"wt.embedded.compaction.pending": "By",
		"wt.embedded.transactions.open":  "{transaction}",
		"wt.embedded.free_pages":         "{page}",
		"wt.embedded.levels.files":       "{file}",
		"wt.embedded.snapshots.open":     "{snapshot}",
		"wt.embedded.iterators.open":     "{iterator}",
	}
	CounterNames = map[string]string{
		"wt.embedded.cache.hits":          "{hit}",
		"wt.embedded.cache.misses":        "{miss}",
		"wt.embedded.cache.evictions":     "{key}",
		"wt.embedded.cache.rejections":    "{key}",
		"wt.embedded.lock.waits":          "{wait}",
		"wt.embedded.lock.wait_time":      "ms",
		"wt.embedded.compactions":         "{compaction}",
		"wt.embedded.compaction.duration": "ms",
		"wt.embedded.flushes":             "{flush}",
		"wt.embedded.transactions":        "{transaction}",
		"wt.embedded.bytes.written":       "By",
		"wt.embedded.bytes.read":          "By",
	}
)

type recorder struct {
	o      metric.Observer
	gauges map[string]metric.Int64ObservableGauge
	counts map[string]metric.Int64ObservableCounter
	attrs  metric.MeasurementOption
}

func (r recorder) Gauge(name string, v int64) {
	if g, ok := r.gauges[name]; ok {
		r.o.ObserveInt64(g, v, r.attrs)
	}
}

func (r recorder) Counter(name string, v int64) {
	if c, ok := r.counts[name]; ok {
		r.o.ObserveInt64(c, v, r.attrs)
	}
}

// Observe registers fn to be read at each metric collection, tagged with the datastore entity
// {db.system.name, db.namespace}. Unregister the returned registration when the store closes.
func Observe(system, namespace string, fn func(Recorder)) (metric.Registration, error) {
	m := otel.Meter(scope)
	r := recorder{
		gauges: map[string]metric.Int64ObservableGauge{},
		counts: map[string]metric.Int64ObservableCounter{},
		attrs: metric.WithAttributes(attribute.String("db.system.name", system),
			attribute.String("db.namespace", namespace)),
	}
	var all []metric.Observable
	for n, u := range GaugeNames {
		g, err := m.Int64ObservableGauge(n, metric.WithUnit(u))
		if err != nil {
			return nil, err
		}
		r.gauges[n] = g
		all = append(all, g)
	}
	for n, u := range CounterNames {
		c, err := m.Int64ObservableCounter(n, metric.WithUnit(u))
		if err != nil {
			return nil, err
		}
		r.counts[n] = c
		all = append(all, c)
	}
	return m.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		rr := r
		rr.o = o
		fn(rr)
		return nil
	}, all...)
}

// FileSize is the size of path in bytes, or -1 when it cannot be read.
func FileSize(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		return -1
	}
	return fi.Size()
}

// ObserveFiles reports the data file and its write-ahead log size, for stores that only expose a
// path (SQLite: path and path+"-wal"; DuckDB: path and path+".wal"). walPath may be empty.
func ObserveFiles(system, path, walPath string) (metric.Registration, error) {
	return Observe(system, path, func(r Recorder) {
		if n := FileSize(path); n >= 0 {
			r.Gauge("wt.embedded.file.size", n)
		}
		if walPath != "" {
			if n := FileSize(walPath); n >= 0 {
				r.Gauge("wt.embedded.wal.size", n)
			}
		}
	})
}
