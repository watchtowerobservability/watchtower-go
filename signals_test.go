package watchtower

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"log/slog"
	"testing"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// sent returns the decompressed bodies the gateway accepted on path.
func (g *fakeGateway) sent(t *testing.T, path string) []byte {
	t.Helper()
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []byte
	for i, p := range g.path {
		if p != path {
			continue
		}
		zr, err := gzip.NewReader(bytes.NewReader(g.body[i]))
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(zr)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, b...)
	}
	return out
}

func TestSlogRecordsCarryTraceIdsAndMetricsExport(t *testing.T) {
	g := newGateway(t)
	shutdown, err := Init(context.Background(), Options{Endpoint: g.URL, IngestKey: "k", ServiceName: "svc"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, span := otel.Tracer("test").Start(context.Background(), "op")
	slog.New(SlogHandler(nil)).InfoContext(ctx, "hello from slog")
	sc := span.SpanContext()
	span.End()
	c, err := otel.Meter("test").Int64Counter("jobs.done")
	if err != nil {
		t.Fatal(err)
	}
	c.Add(context.Background(), 3)
	if err := shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	tid, sid := sc.TraceID(), sc.SpanID()
	logs := g.sent(t, "/v1/logs")
	if !bytes.Contains(logs, []byte("hello from slog")) || !bytes.Contains(logs, tid[:]) || !bytes.Contains(logs, sid[:]) {
		t.Fatal("log record missing its body or the trace and span ids")
	}
	if !bytes.Contains(g.sent(t, "/v1/metrics"), []byte("jobs.done")) {
		t.Fatal("metrics are on by default and must reach /v1/metrics")
	}
}

func TestSignalSwitchesAndDeltaTemporality(t *testing.T) {
	t.Setenv("WATCHTOWER_METRICS", "off")
	t.Setenv("OTEL_LOGS_EXPORTER", "none")
	o, err := Options{IngestKey: "k", ServiceName: "s"}.resolve()
	if err != nil || o.Signals != Traces {
		t.Fatalf("signals %b err %v", o.Signals, err)
	}
	sel := temporality("")
	if sel(sdkmetric.InstrumentKindCounter) != metricdata.DeltaTemporality || sel(sdkmetric.InstrumentKindHistogram) != metricdata.DeltaTemporality {
		t.Fatal("counters and histograms must be delta")
	}
	if sel(sdkmetric.InstrumentKindUpDownCounter) != metricdata.CumulativeTemporality {
		t.Fatal("up-down counters stay cumulative")
	}
	if temporality("cumulative")(sdkmetric.InstrumentKindCounter) != metricdata.CumulativeTemporality {
		t.Fatal("OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE=cumulative must win")
	}
}
