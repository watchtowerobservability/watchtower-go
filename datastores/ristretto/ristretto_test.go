package wtristretto

import (
	"testing"

	"github.com/dgraph-io/ristretto/v2"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/watchtowerobservability/watchtower-go/internal/metrictest"
)

func TestHitsAndMisses(t *testing.T) {
	rd := sdkmetric.NewManualReader()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(rd)))
	c, err := ristretto.NewCache(&ristretto.Config[string, int]{NumCounters: 100, MaxCost: 100, BufferItems: 64, Metrics: true})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Set("a", 1, 1)
	c.Wait()
	c.Get("a")
	c.Get("b")
	reg, err := Observe(c, "sessions")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reg.Unregister() }()
	got := metrictest.Collect(t, rd)
	if got["wt.embedded.cache.hits"] != 1 || got["wt.embedded.cache.misses"] != 1 {
		t.Fatalf("metrics %v", got)
	}
}
