package wtnethttp

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	watchtower "github.com/watchtowerobservability/watchtower-go"
)

func TestHandlerCapturesPanicOnRequestSpan(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr)))
	h := Handler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("kaboom") }), "test")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d", rec.Code)
	}
	spans := sr.Ended()
	if len(spans) != 1 || spans[0].Status().Code != codes.Error || len(spans[0].Events()) != 1 {
		t.Fatalf("want one failed span with an exception event, got %d spans", len(spans))
	}
}

func TestHandlerPassesNormalRequests(t *testing.T) {
	otel.SetTracerProvider(sdktrace.NewTracerProvider())
	h := Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) }), "test")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusTeapot {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestHandlerCapturesRedactedBodiesPerRule(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr)))
	watchtower.SetCaptureRules([]watchtower.CaptureRule{{Route: "/graphql", BodyBytes: 64, GraphQLQuery: true}})
	defer watchtower.SetCaptureRules(nil)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /graphql", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"email":"a@b.io"}}`))
	})
	body := `{"query":"mutation Add { add(password: \"x\") { id } }","password":"hunter2"}`
	Handler(mux, "test").ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/graphql", strings.NewReader(body)))
	attrs := map[string]string{}
	for _, kv := range sr.Ended()[0].Attributes() {
		attrs[string(kv.Key)] = kv.Value.AsString()
	}
	if strings.Contains(attrs["http.request.body"], "hunter2") || strings.Contains(attrs["http.response.body"], "a@b.io") {
		t.Fatalf("not redacted: %v", attrs)
	}
	if attrs["graphql.operation.type"] != "mutation" || attrs["graphql.operation.name"] != "Add" || attrs["graphql.document"] == "" {
		t.Fatalf("graphql: %v", attrs)
	}
	if len(attrs["http.request.body"]) > 64 {
		t.Fatalf("not capped: %d", len(attrs["http.request.body"]))
	}
}
