package wtbadger

import (
	"context"
	"testing"

	"github.com/dgraph-io/badger/v4"
	"go.opentelemetry.io/otel"
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
	db, err := badger.Open(badger.DefaultOptions(t.TempDir()).WithLogger(nil))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := Update(ctx, db, func(txn *badger.Txn) error { return txn.Set([]byte("k"), []byte("v")) }); err != nil {
		t.Fatal(err)
	}
	if err := RunValueLogGC(ctx, db, 0.5); err != nil && err != badger.ErrNoRewrite {
		t.Fatal(err)
	}
	for _, s := range sr.Ended() {
		if s.Status().Code != 0 && s.Name() == "value_log_gc" {
			t.Fatal("ErrNoRewrite must not mark the span as failed")
		}
	}
	reg, err := Observe(db)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reg.Unregister() }()
	if _, ok := metrictest.Collect(t, rd)["wt.embedded.wal.size"]; !ok {
		t.Fatal("value-log size missing")
	}
}
