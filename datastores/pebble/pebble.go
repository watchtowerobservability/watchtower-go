// Package wtpebble instruments github.com/cockroachdb/pebble/v2: spans for Get/Set/Delete/Apply and
// the engine's own metrics (disk and WAL size, memtable, block cache, compactions, flushes,
// compaction debt, open snapshots and iterators). Keys and values are never recorded.
package wtpebble

import (
	"context"
	"errors"
	"io"

	"github.com/cockroachdb/pebble/v2"
	"go.opentelemetry.io/otel/metric"

	wtembedded "github.com/watchtowerobservability/watchtower-go/embedded"
)

const system = "pebble"

// DB pairs a pebble handle with its namespace (data directory), which pebble does not expose.
type DB struct {
	*pebble.DB
	Namespace string
}

func (d DB) span(ctx context.Context, op string, call func() error) error {
	return wtembedded.Span(ctx, wtembedded.Op{System: system, Namespace: d.Namespace, Operation: op},
		func(context.Context) error { return call() })
}

// Get is pebble Get in a "get" span; pebble.ErrNotFound is returned but does not fail the span.
func (d DB) Get(ctx context.Context, key []byte) (v []byte, c io.Closer, err error) {
	_ = d.span(ctx, "get", func() error {
		v, c, err = d.DB.Get(key)
		if errors.Is(err, pebble.ErrNotFound) {
			return nil
		}
		return err
	})
	return v, c, err
}

// Set is pebble Set in a "set" span.
func (d DB) Set(ctx context.Context, key, value []byte, o *pebble.WriteOptions) error {
	return d.span(ctx, "set", func() error { return d.DB.Set(key, value, o) })
}

// Delete is pebble Delete in a "delete" span.
func (d DB) Delete(ctx context.Context, key []byte, o *pebble.WriteOptions) error {
	return d.span(ctx, "delete", func() error { return d.DB.Delete(key, o) })
}

// Apply commits a batch in an "apply" span.
func (d DB) Apply(ctx context.Context, b *pebble.Batch, o *pebble.WriteOptions) error {
	return d.span(ctx, "apply", func() error { return d.DB.Apply(b, o) })
}

// Observe reports pebble's metrics as wt.embedded.*.
func (d DB) Observe() (metric.Registration, error) {
	return wtembedded.Observe(system, d.Namespace, func(r wtembedded.Recorder) {
		m := d.Metrics()
		r.Gauge("wt.embedded.file.size", int64(m.DiskSpaceUsage()))
		r.Gauge("wt.embedded.wal.size", int64(m.WAL.PhysicalSize))
		r.Gauge("wt.embedded.memory.used", int64(m.MemTable.Size))
		r.Gauge("wt.embedded.cache.size", m.BlockCache.Size)
		r.Counter("wt.embedded.cache.hits", m.BlockCache.Hits)
		r.Counter("wt.embedded.cache.misses", m.BlockCache.Misses)
		r.Counter("wt.embedded.compactions", m.Compact.Count)
		r.Counter("wt.embedded.compaction.duration", m.Compact.Duration.Milliseconds())
		r.Gauge("wt.embedded.compaction.pending", int64(m.Compact.EstimatedDebt))
		r.Counter("wt.embedded.flushes", m.Flush.Count)
		r.Gauge("wt.embedded.snapshots.open", int64(m.Snapshots.Count))
		r.Gauge("wt.embedded.iterators.open", m.TableIters)
		var files int64
		for _, l := range m.Levels {
			files += l.TablesCount
		}
		r.Gauge("wt.embedded.levels.files", files)
	})
}
