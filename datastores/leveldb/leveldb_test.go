package wtleveldb

import (
	"context"
	"testing"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/storage"
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
	l, err := leveldb.Open(storage.NewMemStorage(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	db := DB{DB: l, Namespace: "mem"}
	ctx := context.Background()
	if err := db.Put(ctx, []byte("k"), []byte("v"), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Get(ctx, []byte("nope"), nil); err != leveldb.ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if len(sr.Ended()) != 2 || sr.Ended()[1].Status().Code == codes.Error {
		t.Fatal("expected put and get spans, get not failed")
	}
	reg, err := db.Observe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reg.Unregister() }()
	if _, ok := metrictest.Collect(t, rd)["wt.embedded.lock.waits"]; !ok {
		t.Fatal("write-delay metric missing")
	}
}
