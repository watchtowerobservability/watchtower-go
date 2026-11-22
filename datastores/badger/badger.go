// Package wtbadger instruments github.com/dgraph-io/badger/v4: a span per View/Update transaction
// and LSM/value-log size, block-cache and compaction-debt metrics. Keys and values are never recorded.
package wtbadger

import (
	"context"

	"github.com/dgraph-io/badger/v4"
	"go.opentelemetry.io/otel/metric"

	wtembedded "github.com/watchtowerobservability/watchtower-go/embedded"
)

const system = "badger"

func span(ctx context.Context, db *badger.DB, op string, call func() error) error {
	return wtembedded.Span(ctx, wtembedded.Op{System: system, Namespace: db.Opts().Dir, Operation: op},
		func(context.Context) error { return call() })
}

// View is db.View inside a "view" span.
func View(ctx context.Context, db *badger.DB, fn func(*badger.Txn) error) error {
	return span(ctx, db, "view", func() error { return db.View(fn) })
}

// Update is db.Update inside an "update" span.
func Update(ctx context.Context, db *badger.DB, fn func(*badger.Txn) error) error {
	return span(ctx, db, "update", func() error { return db.Update(fn) })
}

// RunValueLogGC is db.RunValueLogGC inside a "value_log_gc" span; badger.ErrNoRewrite is not an error.
func RunValueLogGC(ctx context.Context, db *badger.DB, discardRatio float64) error {
	var err error
	_ = span(ctx, db, "value_log_gc", func() error {
		if err = db.RunValueLogGC(discardRatio); err == badger.ErrNoRewrite {
			return nil
		}
		return err
	})
	return err
}

// Observe reports LSM size (file.size), value-log size (wal.size), table count, compaction debt
// (bytes above level targets) and block-cache hits, misses and evictions when the cache is on.
func Observe(db *badger.DB) (metric.Registration, error) {
	return wtembedded.Observe(system, db.Opts().Dir, func(r wtembedded.Recorder) {
		lsm, vlog := db.Size()
		r.Gauge("wt.embedded.file.size", lsm)
		r.Gauge("wt.embedded.wal.size", vlog)
		var tables, debt int64
		for _, l := range db.Levels() {
			tables += int64(l.NumTables)
			if l.TargetSize > 0 && l.Size > l.TargetSize {
				debt += l.Size - l.TargetSize
			}
		}
		r.Gauge("wt.embedded.levels.files", tables)
		r.Gauge("wt.embedded.compaction.pending", debt)
		if m := db.BlockCacheMetrics(); m != nil {
			r.Counter("wt.embedded.cache.hits", int64(m.Hits()))
			r.Counter("wt.embedded.cache.misses", int64(m.Misses()))
			r.Counter("wt.embedded.cache.evictions", int64(m.KeysEvicted()))
		}
	})
}
