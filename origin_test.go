package watchtower

import (
	"context"
	"strings"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestCodeOriginStampsTheCallerOfStart(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(codeOrigin{}), sdktrace.WithSpanProcessor(rec))
	_, span := tp.Tracer("t").Start(context.Background(), "op")
	span.End()
	got := map[string]string{}
	for _, a := range rec.Ended()[0].Attributes() {
		got[string(a.Key)] = a.Value.Emit()
	}
	if !strings.HasSuffix(got["code.filepath"], "origin_test.go") || !strings.HasSuffix(got["code.function"], "TestCodeOriginStampsTheCallerOfStart") || got["code.lineno"] == "" {
		t.Fatalf("got %v", got)
	}
}
