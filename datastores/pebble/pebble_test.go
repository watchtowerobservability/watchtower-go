package wtpebble

import (
	"context"
	"testing"

	"github.com/cockroachdb/pebble/v2"
	"github.com/cockroachdb/pebble/v2/vfs"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/watchtowerobservability/watchtower-go/internal/metrictest"
)

func TestSpansAndMetrics(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr)))
	rd := sdkmetric.NewManualReader()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(rd)))
	p, err := pebble.Open("mem", &pebble.Options{FS: vfs.NewMem()})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	db := DB{DB: p, Namespace: "mem"}
	ctx := context.Background()
	if err := db.Set(ctx, []byte("k"), []byte("v"), pebble.Sync); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.Get(ctx, []byte("missing")); err != pebble.ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	for _, s := range sr.Ended() {
		if s.Status().Code == codes.Error {
			t.Fatalf("span %s failed; not-found is not an error", s.Name())
		}
	}
	reg, err := db.Observe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reg.Unregister() }()
	if _, ok := metrictest.Collect(t, rd)["wt.embedded.cache.hits"]; !ok {
		t.Fatal("cache hits missing")
	}
}
