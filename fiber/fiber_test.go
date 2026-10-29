package wtfiber

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
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
