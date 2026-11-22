package wtecho

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	watchtower "github.com/watchtowerobservability/watchtower-go"
)

func TestPanicAndServerErrorsAreCapturedClientErrorsAreNot(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr)))
	e := echo.New()
	Use(e, "svc")
	e.GET("/panic", func(echo.Context) error { panic("x") })
	e.GET("/err", func(echo.Context) error { return errors.New("db down") })
	e.GET("/missing", func(echo.Context) error { return echo.ErrNotFound })

	codes := map[string]int{}
	for _, p := range []string{"/panic", "/err", "/missing"} {
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		codes[p] = rec.Code
	}
	if codes["/panic"] != 500 || codes["/err"] != 500 || codes["/missing"] != 404 {
		t.Fatalf("statuses %v", codes)
	}
	var withEvent int
	for _, s := range sr.Ended() {
		withEvent += len(s.Events())
	}
	if withEvent != 2 {
		t.Fatalf("want 2 exception events (panic, 500), got %d", withEvent)
	}
}

func TestBodiesCapturedPerRule(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr)))
	watchtower.SetCaptureRules([]watchtower.CaptureRule{{Route: "/login/:id", BodyBytes: 200}})
	defer watchtower.SetCaptureRules(nil)
	e := echo.New()
	Use(e, "svc")
	e.POST("/login/:id", func(c echo.Context) error {
		_, _ = io.ReadAll(c.Request().Body)
		return c.JSON(http.StatusOK, map[string]string{"email": "a@b.io"})
	})
	req := httptest.NewRequest(http.MethodPost, "/login/1", strings.NewReader(`{"password":"hunter2"}`))
	req.Header.Set("Content-Type", "application/json")
	e.ServeHTTP(httptest.NewRecorder(), req)
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
