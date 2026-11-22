package wtfiber

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	watchtower "github.com/watchtowerobservability/watchtower-go"
)

func TestSpanPanicAndPropagation(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr)))
	otel.SetTextMapPropagator(propagation.TraceContext{})
	app := fiber.New()
	app.Use(Middleware())
	app.Get("/users/:id", func(c *fiber.Ctx) error { return c.SendString("ok") })
	app.Get("/panic", func(*fiber.Ctx) error { panic("x") })

	req := httptest.NewRequest("GET", "/users/1", nil)
	req.Header.Set("traceparent", "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01")
	if resp, err := app.Test(req); err != nil || resp.StatusCode != 200 {
		t.Fatalf("ok route: %v %v", resp, err)
	}
	resp, err := app.Test(httptest.NewRequest("GET", "/panic", nil))
	if err != nil || resp.StatusCode != 500 {
		t.Fatalf("panic route: %v %v", resp, err)
	}

	spans := sr.Ended()
	if len(spans) != 2 {
		t.Fatalf("%d spans", len(spans))
	}
	if spans[0].Name() != "GET /users/:id" || spans[0].SpanContext().TraceID().String() != "0af7651916cd43dd8448eb211c80319c" {
		t.Fatalf("span %q trace %s", spans[0].Name(), spans[0].SpanContext().TraceID())
	}
	if len(spans[1].Events()) != 1 {
		t.Fatal("panic not captured")
	}
}

func TestBodiesCapturedPerRule(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr)))
	watchtower.SetCaptureRules([]watchtower.CaptureRule{{Route: "/login/:id", BodyBytes: 200}})
	defer watchtower.SetCaptureRules(nil)
	app := fiber.New()
	app.Use(Middleware())
	app.Post("/login/:id", func(c *fiber.Ctx) error { return c.JSON(fiber.Map{"email": "a@b.io"}) })
	req := httptest.NewRequest("POST", "/login/1", strings.NewReader(`{"password":"hunter2"}`))
	req.Header.Set("Content-Type", "application/json")
	if _, err := app.Test(req); err != nil {
		t.Fatal(err)
	}
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
