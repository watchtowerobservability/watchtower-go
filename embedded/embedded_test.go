package wtembedded

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/watchtowerobservability/watchtower-go/internal/metrictest"
)

func TestSpanCarriesSemconvAndError(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr)))
	boom := errors.New("boom")
	err := Span(context.Background(), Op{System: "bbolt", Namespace: "/d.db", Operation: "update", Collection: "jobs"},
		func(context.Context) error { return boom })
	if err != boom {
		t.Fatalf("error not passed through: %v", err)
	}
	s := sr.Ended()[0]
	if s.Name() != "update jobs" || s.Status().Code != codes.Error {
		t.Fatalf("span %q status %v", s.Name(), s.Status())
	}
	want := map[string]string{"db.system.name": "bbolt", "db.namespace": "/d.db", "db.operation.name": "update", "db.collection.name": "jobs"}
	for _, kv := range s.Attributes() {
		delete(want, string(kv.Key))
	}
	if len(want) != 0 {
		t.Fatalf("missing attributes %v", want)
	}
}

func TestObserveFilesAndUnknownNames(t *testing.T) {
	rd := sdkmetric.NewManualReader()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(rd)))
	dir := t.TempDir()
	db := filepath.Join(dir, "app.db")
	if err := os.WriteFile(db, make([]byte, 4096), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(db+"-wal", make([]byte, 100), 0o600); err != nil {
		t.Fatal(err)
	}
	reg, err := ObserveFiles("sqlite", db, db+"-wal")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reg.Unregister() }()
	if _, err := Observe("x", "y", func(r Recorder) { r.Gauge("not.ours", 1); r.Counter("wt.embedded.cache.hits", 7) }); err != nil {
		t.Fatal(err)
	}
	got := metrictest.Collect(t, rd)
	if got["wt.embedded.file.size"] != 4096 || got["wt.embedded.wal.size"] != 100 || got["wt.embedded.cache.hits"] != 7 {
		t.Fatalf("got %v", got)
	}
	if _, ok := got["not.ours"]; ok {
		t.Fatal("unknown name must be ignored")
	}
}
