package watchtower

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type fakeGateway struct {
	*httptest.Server
	down atomic.Bool
	mu   sync.Mutex
	ok   int // requests answered 200
	auth []string
	path []string
}

func newGateway(t *testing.T) *fakeGateway {
	t.Helper()
	g := &fakeGateway{}
	g.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if g.down.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		g.mu.Lock()
		g.ok++
		g.auth = append(g.auth, r.Header.Get("Authorization"))
		g.path = append(g.path, r.URL.Path)
		g.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(g.Close)
	return g
}

func (g *fakeGateway) accepted() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.ok
}

func emit(name string) {
	_, span := otel.Tracer("test").Start(context.Background(), name)
	span.End()
}

func TestInitSendsTracesWithBearerKey(t *testing.T) {
	g := newGateway(t)
	shutdown, err := Init(context.Background(), Options{Endpoint: g.URL, IngestKey: "wti_test", ServiceName: "svc", Environment: "test"})
	if err != nil {
		t.Fatal(err)
	}
	emit("hello")
	if err := shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if g.accepted() != 1 || g.auth[0] != "Bearer wti_test" || g.path[0] != "/v1/traces" {
		t.Fatalf("got ok=%d auth=%v paths=%v", g.accepted(), g.auth, g.path)
	}
}

func TestInitReadsEnvironment(t *testing.T) {
	g := newGateway(t)
	t.Setenv("WT_OTLP_ENDPOINT", g.URL+"/")
	t.Setenv("WT_INGEST_KEY", "wti_env")
	t.Setenv("WT_SERVICE", "envsvc")
	shutdown, err := Init(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	emit("env")
	_ = shutdown(context.Background())
	if g.accepted() < 1 || g.auth[0] != "Bearer wti_env" {
		t.Fatalf("env config not used: %v", g.auth)
	}
}

func TestDefaultEndpoint(t *testing.T) {
	o, err := Options{IngestKey: "k", ServiceName: "s"}.resolve()
	if err != nil || o.Endpoint != "http://localhost:4318" {
		t.Fatalf("endpoint %q err %v", o.Endpoint, err)
	}
}

func TestOfflineBufferReplaysAfterRecovery(t *testing.T) {
	g := newGateway(t)
	opt := Options{Endpoint: g.URL, IngestKey: "wti_test", ServiceName: "svc", BufferDir: t.TempDir()}

	g.down.Store(true)
	shutdown, err := Init(context.Background(), opt)
	if err != nil {
		t.Fatal(err)
	}
	emit("offline")
	if err := shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if g.accepted() != 0 {
		t.Fatal("gateway was down, nothing may be accepted")
	}

	g.down.Store(false)
	shutdown, err = Init(context.Background(), opt)
	if err != nil {
		t.Fatal(err)
	}
	emit("online")
	if err := shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if g.accepted() != 2 {
		t.Fatalf("accepted %d requests, want 2 (live + replayed)", g.accepted())
	}
	for _, a := range g.auth {
		if a != "Bearer wti_test" {
			t.Fatalf("replayed request lost the key header: %v", g.auth)
		}
	}
}

func TestShutdownFlushes(t *testing.T) {
	g := newGateway(t)
	shutdown, err := Init(context.Background(), Options{Endpoint: g.URL, IngestKey: "k", ServiceName: "svc", Signals: Traces | Logs | Metrics})
	if err != nil {
		t.Fatal(err)
	}
	emit("pending in the batcher")
	if g.accepted() != 0 {
		t.Fatal("batch should not be sent before shutdown")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if g.accepted() < 1 {
		t.Fatal("shutdown did not flush")
	}
}

func TestInitRequiresKeyAndName(t *testing.T) {
	t.Setenv("WT_INGEST_KEY", "")
	t.Setenv("WT_SERVICE", "")
	if _, err := Init(context.Background(), Options{}); err == nil {
		t.Fatal("want an error for empty options")
	}
}

func TestKeyIsMasked(t *testing.T) {
	o := Options{IngestKey: "wti_supersecret", ServiceName: "s"}
	var sb strings.Builder
	slog.New(slog.NewTextHandler(&sb, nil)).Info("cfg", "opts", o)
	for _, out := range []string{o.String(), fmt.Sprintf("%v %+v %#v", o, o, o), sb.String()} {
		if strings.Contains(out, "supersecret") {
			t.Fatalf("key leaked: %s", out)
		}
	}
}

func recorder(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	sr := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr)))
	return sr
}

func TestCaptureErrorRecordsEventOnActiveSpan(t *testing.T) {
	sr := recorder(t)
	ctx, span := otel.Tracer("t").Start(context.Background(), "op")
	id := CaptureError(ctx, fmt.Errorf("boom"), attribute.String("wt.tenant", "x"), attribute.String("user", "u"))
	span.End()
	if id != span.SpanContext().TraceID().String() {
		t.Fatalf("returned %q, want the span's trace id", id)
	}
	s := sr.Ended()[0]
	if s.Status().Code != codes.Error || len(s.Events()) != 1 || s.Events()[0].Name != "exception" {
		t.Fatalf("span not marked: %+v", s.Events())
	}
	got := map[string]string{}
	for _, kv := range s.Events()[0].Attributes {
		got[string(kv.Key)] = kv.Value.Emit()
	}
	if got["exception.message"] != "boom" || got["user"] != "u" || !strings.Contains(got["exception.stacktrace"], "TestCaptureErrorRecordsEventOnActiveSpan") {
		t.Fatalf("attrs: %v", got)
	}
	if _, bad := got["wt.tenant"]; bad {
		t.Fatal("wt.* attribute was not dropped")
	}
}

func TestCaptureWithoutSpanStartsOne(t *testing.T) {
	sr := recorder(t)
	if id := CaptureError(context.Background(), fmt.Errorf("lonely")); len(id) != 32 || id == strings.Repeat("0", 32) {
		t.Fatalf("trace id %q", id)
	}
	if n := len(sr.Ended()); n != 1 || sr.Ended()[0].Name() != "error" {
		t.Fatalf("want one error span, got %d", n)
	}
	if CaptureError(context.Background(), nil) != "" {
		t.Fatal("nil error must be ignored")
	}
}

func TestCapturePanicAndRedact(t *testing.T) {
	sr := recorder(t)
	g := newGateway(t)
	shutdown, err := Init(context.Background(), Options{Endpoint: g.URL, IngestKey: "k", ServiceName: "s", Signals: Metrics,
		Redact: func(_, v string) string { return strings.ReplaceAll(v, "hunter2", "[redacted]") }})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = shutdown(context.Background()); setRedact(nil) }()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))) // Init (metrics only) left traces alone, keep explicit
	func() {
		defer func() {
			if r := recover(); r != nil {
				CapturePanic(context.Background(), r)
			}
		}()
		panic("password hunter2")
	}()
	var msg, esc string
	for _, kv := range sr.Ended()[0].Events()[0].Attributes {
		switch kv.Key {
		case "exception.message":
			msg = kv.Value.Emit()
		case "exception.escaped":
			esc = kv.Value.Emit()
		}
	}
	if msg != "password [redacted]" || esc != "true" {
		t.Fatalf("message %q escaped %q", msg, esc)
	}
}

func TestSlogHandlerFansOut(t *testing.T) {
	var sb strings.Builder
	l := slog.New(SlogHandler(slog.NewTextHandler(&sb, nil)))
	l.Info("hello", "k", "v")
	if !strings.Contains(sb.String(), "hello") {
		t.Fatalf("next handler got nothing: %q", sb.String())
	}
}
