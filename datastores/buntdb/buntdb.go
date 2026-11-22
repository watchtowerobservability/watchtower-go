// Package wtbuntdb instruments github.com/tidwall/buntdb: a span per View/Update transaction and
// key-count and file-size metrics. Keys and values are never recorded.
package wtbuntdb

import (
	"context"

	"github.com/tidwall/buntdb"
	"go.opentelemetry.io/otel/metric"

	wtembedded "github.com/watchtowerobservability/watchtower-go/embedded"
)

const system = "buntdb"

// DB pairs a buntdb handle with its path (":memory:" for in-memory).
type DB struct {
	*buntdb.DB
	Path string
}

// View is db.View inside a "view" span.
func (d DB) View(ctx context.Context, fn func(*buntdb.Tx) error) error {
	return wtembedded.Span(ctx, wtembedded.Op{System: system, Namespace: d.Path, Operation: "view"},
		func(context.Context) error { return d.DB.View(fn) })
}

// Update is db.Update inside an "update" span.
func (d DB) Update(ctx context.Context, fn func(*buntdb.Tx) error) error {
	return wtembedded.Span(ctx, wtembedded.Op{System: system, Namespace: d.Path, Operation: "update"},
		func(context.Context) error { return d.DB.Update(fn) })
}

// Observe reports the key count and, for file-backed databases, the append-only file size.
func (d DB) Observe() (metric.Registration, error) {
	return wtembedded.Observe(system, d.Path, func(r wtembedded.Recorder) {
		_ = d.DB.View(func(tx *buntdb.Tx) error {
			if n, err := tx.Len(); err == nil {
				r.Gauge("wt.embedded.keys", int64(n))
			}
			return nil
		})
		if d.Path != ":memory:" {
			if n := wtembedded.FileSize(d.Path); n >= 0 {
				r.Gauge("wt.embedded.file.size", n)
			}
		}
	})
}
