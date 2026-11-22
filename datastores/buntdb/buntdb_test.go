package wtbuntdb

import (
	"context"
	"testing"

	"github.com/tidwall/buntdb"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/watchtowerobservability/watchtower-go/internal/metrictest"
)

func TestSpansAndKeyCount(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr)))
	rd := sdkmetric.NewManualReader()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(rd)))
	b, err := buntdb.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	db := DB{DB: b, Path: ":memory:"}
	if err := db.Update(context.Background(), func(tx *buntdb.Tx) error {
		_, _, err := tx.Set("a", "1", nil)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(sr.Ended()) != 1 || sr.Ended()[0].Name() != "update" {
		t.Fatal("expected one update span")
	}
	reg, err := db.Observe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reg.Unregister() }()
	if got := metrictest.Collect(t, rd)["wt.embedded.keys"]; got != 1 {
		t.Fatalf("keys %d", got)
	}
}
