package wtbbolt

import (
	"context"
	"path/filepath"
	"testing"

	bolt "go.etcd.io/bbolt"
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
	db, err := bolt.Open(filepath.Join(t.TempDir(), "t.db"), 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := Update(ctx, db, func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte("jobs"))
		if err != nil {
			return err
		}
		return b.Put([]byte("k"), []byte("v"))
	}); err != nil {
		t.Fatal(err)
	}
	if err := View(ctx, db, func(*bolt.Tx) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if len(sr.Ended()) != 2 || sr.Ended()[0].Name() != "update" || sr.Ended()[1].Name() != "view" {
		t.Fatalf("spans %v", sr.Ended())
	}
	reg, err := Observe(db)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reg.Unregister() }()
	got := metrictest.Collect(t, rd)
	if got["wt.embedded.file.size"] <= 0 || got["wt.embedded.transactions"] < 1 {
		t.Fatalf("metrics %v", got)
	}
}
