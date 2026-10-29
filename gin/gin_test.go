package wtgin

import (
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestPanicAndContextErrorsAreCaptured(t *testing.T) {
	gin.SetMode(gin.TestMode)
	sr := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr)))
	r := gin.New()
	Use(r, "svc")
	r.GET("/panic", func(*gin.Context) { panic("x") })
	r.GET("/err", func(c *gin.Context) { _ = c.Error(errors.New("bad")); c.Status(400) })

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/panic", nil))
	if rec.Code != 500 {
		t.Fatalf("panic status %d", rec.Code)
	}
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/err", nil))

	spans := sr.Ended()
	if len(spans) != 2 {
		t.Fatalf("%d spans", len(spans))
	}
	for i, s := range spans {
		if len(s.Events()) != 1 || s.Events()[0].Name != "exception" {
			t.Fatalf("span %d events: %v", i, s.Events())
		}
	}
}
