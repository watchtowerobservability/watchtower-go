package wtgin

import (
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	watchtower "github.com/watchtowerobservability/watchtower-go"
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

func TestBodiesCapturedPerRule(t *testing.T) {
	gin.SetMode(gin.TestMode)
	sr := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr)))
	watchtower.SetCaptureRules([]watchtower.CaptureRule{{Route: "/login/:id", BodyBytes: 200}})
	defer watchtower.SetCaptureRules(nil)
	r := gin.New()
	Use(r, "svc")
	r.POST("/login/:id", func(c *gin.Context) {
		_, _ = io.ReadAll(c.Request.Body)
		c.JSON(200, gin.H{"email": "a@b.io"})
	})
	req := httptest.NewRequest("POST", "/login/1", strings.NewReader(`{"password":"hunter2"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(httptest.NewRecorder(), req)
	checkRedacted(t, spanAttrs(sr))
}

func spanAttrs(sr *tracetest.SpanRecorder) map[string]string {
	attrs := map[string]string{}
	for _, kv := range sr.Ended()[0].Attributes() {
		attrs[string(kv.Key)] = kv.Value.Emit()
	}
	return attrs
}

func checkRedacted(t *testing.T, attrs map[string]string) {
	t.Helper()
	req, res := attrs["http.request.body"], attrs["http.response.body"]
	if req == "" || res == "" || strings.Contains(req, "hunter2") || strings.Contains(res, "a@b.io") {
		t.Fatalf("bodies not captured or not redacted: %v", attrs)
	}
}
