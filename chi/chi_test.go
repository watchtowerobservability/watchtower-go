package wtchi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestSpanNamedAfterRoutePattern(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr)))
	r := chi.NewRouter()
	r.Use(Middleware)
	r.Get("/users/{id}", func(http.ResponseWriter, *http.Request) {})
	r.Get("/boom", func(http.ResponseWriter, *http.Request) { panic("x") })

	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/users/42", nil))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/boom", nil))

	if rec.Code != 500 {
		t.Fatalf("panic status %d", rec.Code)
	}
	spans := sr.Ended()
	if len(spans) != 2 || spans[0].Name() != "GET /users/{id}" {
		t.Fatalf("spans: %d first=%q", len(spans), spans[0].Name())
	}
	if len(spans[1].Events()) != 1 {
		t.Fatal("panic not captured")
	}
}
