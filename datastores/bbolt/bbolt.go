// Package wtbbolt instruments go.etcd.io/bbolt: a span per View/Update/Batch transaction and
// file, free-page and open-transaction metrics. Keys and values are never recorded.
package wtbbolt

import (
	"context"

	bolt "go.etcd.io/bbolt"
	"go.opentelemetry.io/otel/metric"

	wtembedded "github.com/watchtowerobservability/watchtower-go/embedded"
)

const system = "bbolt"

func run(ctx context.Context, db *bolt.DB, op string, call func(func(*bolt.Tx) error) error, fn func(*bolt.Tx) error) error {
	return wtembedded.Span(ctx, wtembedded.Op{System: system, Namespace: db.Path(), Operation: op},
		func(context.Context) error { return call(fn) })
}

// View is db.View inside a "view" span.
func View(ctx context.Context, db *bolt.DB, fn func(*bolt.Tx) error) error {
	return run(ctx, db, "view", db.View, fn)
}

// Update is db.Update inside an "update" span.
func Update(ctx context.Context, db *bolt.DB, fn func(*bolt.Tx) error) error {
	return run(ctx, db, "update", db.Update, fn)
}

// Batch is db.Batch inside a "batch" span.
func Batch(ctx context.Context, db *bolt.DB, fn func(*bolt.Tx) error) error {
	return run(ctx, db, "batch", db.Batch, fn)
}

// Observe reports file size, free pages, open and total read transactions and page writes.
func Observe(db *bolt.DB) (metric.Registration, error) {
	return wtembedded.Observe(system, db.Path(), func(r wtembedded.Recorder) {
		s := db.Stats()
		if n := wtembedded.FileSize(db.Path()); n >= 0 {
			r.Gauge("wt.embedded.file.size", n)
		}
		r.Gauge("wt.embedded.free_pages", int64(s.FreePageN+s.PendingPageN))
		r.Gauge("wt.embedded.transactions.open", int64(s.OpenTxN))
		r.Counter("wt.embedded.transactions", int64(s.TxN))
		r.Counter("wt.embedded.bytes.written", s.TxStats.GetWrite()*int64(db.Info().PageSize))
	})
}
