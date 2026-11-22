// Package wtleveldb instruments github.com/syndtr/goleveldb: spans for Get/Put/Delete/Write and
// metrics for level sizes, write stalls (lock waits), compaction time, IO and open snapshots and
// iterators. Keys and values are never recorded.
package wtleveldb

import (
	"context"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/opt"
	"go.opentelemetry.io/otel/metric"

	wtembedded "github.com/watchtowerobservability/watchtower-go/embedded"
)

const system = "leveldb"

// DB pairs a goleveldb handle with its namespace (data directory).
type DB struct {
	*leveldb.DB
	Namespace string
}

func (d DB) span(ctx context.Context, op string, call func() error) error {
	return wtembedded.Span(ctx, wtembedded.Op{System: system, Namespace: d.Namespace, Operation: op},
		func(context.Context) error { return call() })
}

// Get in a "get" span; leveldb.ErrNotFound is returned but does not fail the span.
func (d DB) Get(ctx context.Context, key []byte, ro *opt.ReadOptions) (v []byte, err error) {
	_ = d.span(ctx, "get", func() error {
		if v, err = d.DB.Get(key, ro); err == leveldb.ErrNotFound {
			return nil
		}
		return err
	})
	return v, err
}

// Put in a "put" span.
func (d DB) Put(ctx context.Context, key, value []byte, wo *opt.WriteOptions) error {
	return d.span(ctx, "put", func() error { return d.DB.Put(key, value, wo) })
}

// Delete in a "delete" span.
func (d DB) Delete(ctx context.Context, key []byte, wo *opt.WriteOptions) error {
	return d.span(ctx, "delete", func() error { return d.DB.Delete(key, wo) })
}

// Write commits a batch in a "write" span.
func (d DB) Write(ctx context.Context, b *leveldb.Batch, wo *opt.WriteOptions) error {
	return d.span(ctx, "write", func() error { return d.DB.Write(b, wo) })
}

// Observe reports goleveldb's DBStats as wt.embedded.*.
func (d DB) Observe() (metric.Registration, error) {
	return wtembedded.Observe(system, d.Namespace, func(r wtembedded.Recorder) {
		var s leveldb.DBStats
		if d.Stats(&s) != nil {
			return
		}
		var size, files, compMs int64
		for i := range s.LevelSizes {
			size += s.LevelSizes[i]
		}
		for _, n := range s.LevelTablesCounts {
			files += int64(n)
		}
		for _, du := range s.LevelDurations {
			compMs += du.Milliseconds()
		}
		r.Gauge("wt.embedded.file.size", size)
		r.Gauge("wt.embedded.levels.files", files)
		r.Gauge("wt.embedded.cache.size", int64(s.BlockCacheSize))
		r.Gauge("wt.embedded.snapshots.open", int64(s.AliveSnapshots))
		r.Gauge("wt.embedded.iterators.open", int64(s.AliveIterators))
		r.Counter("wt.embedded.lock.waits", int64(s.WriteDelayCount))
		r.Counter("wt.embedded.lock.wait_time", s.WriteDelayDuration.Milliseconds())
		r.Counter("wt.embedded.compaction.duration", compMs)
		r.Counter("wt.embedded.bytes.written", int64(s.IOWrite))
		r.Counter("wt.embedded.bytes.read", int64(s.IORead))
	})
}
