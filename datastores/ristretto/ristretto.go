// Package wtristretto reports github.com/dgraph-io/ristretto/v2 cache health: hits, misses,
// evictions, rejected sets and the cost in use. Create the cache with Config.Metrics = true.
// Cache calls get no spans (too hot to trace one by one).
package wtristretto

import (
	"github.com/dgraph-io/ristretto/v2"
	"go.opentelemetry.io/otel/metric"

	wtembedded "github.com/watchtowerobservability/watchtower-go/embedded"
)

// Observe reports c's metrics as wt.embedded.* under db.system.name "ristretto" and db.namespace name.
func Observe[K ristretto.Key, V any](c *ristretto.Cache[K, V], name string) (metric.Registration, error) {
	return wtembedded.Observe("ristretto", name, func(r wtembedded.Recorder) {
		m := c.Metrics
		if m == nil {
			return
		}
		r.Counter("wt.embedded.cache.hits", int64(m.Hits()))
		r.Counter("wt.embedded.cache.misses", int64(m.Misses()))
		r.Counter("wt.embedded.cache.evictions", int64(m.KeysEvicted()))
		r.Counter("wt.embedded.cache.rejections", int64(m.SetsRejected()+m.SetsDropped()))
		r.Gauge("wt.embedded.memory.used", int64(m.CostAdded()-m.CostEvicted()))
		r.Gauge("wt.embedded.keys", int64(m.KeysAdded()-m.KeysEvicted()))
	})
}
