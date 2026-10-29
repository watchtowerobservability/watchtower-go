package wtecho

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
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
