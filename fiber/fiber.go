// Package wtfiber instruments Fiber v2: server spans with W3C context extraction, panic and
// handler error capture, and payload capture per the capture rules.
package wtfiber

import (
	"errors"
	"net/http"

	"github.com/gofiber/fiber/v2"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	watchtower "github.com/watchtowerobservability/watchtower-go"
)

// Middleware is for app.Use. Handlers read the span through c.UserContext().
func Middleware() fiber.Handler {
	return func(c *fiber.Ctx) (err error) {
		hdr := http.Header{}
		c.Request().Header.VisitAll(func(k, v []byte) { hdr.Add(string(k), string(v)) })
		ctx := otel.GetTextMapPropagator().Extract(c.UserContext(), propagation.HeaderCarrier(hdr))
		ctx, span := otel.Tracer("github.com/watchtowerobservability/watchtower-go/fiber").Start(ctx, c.Method()+" "+c.Path(),
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(attribute.String("http.request.method", c.Method()), attribute.String("url.path", c.Path()),
				attribute.String("server.address", c.Hostname())))
		defer span.End()
		c.SetUserContext(ctx)
		defer func() {
			panicked := false
			if rec := recover(); rec != nil {
				panicked = true
				watchtower.CapturePanic(ctx, rec)
				err = fiber.ErrInternalServerError
			}
			status := c.Response().StatusCode()
			var fe *fiber.Error
			if errors.As(err, &fe) {
				status = fe.Code
			} else if err != nil {
				status = http.StatusInternalServerError
			}
			if err != nil && !panicked && status >= http.StatusInternalServerError {
				watchtower.CaptureError(ctx, err)
			}
			route := c.Route().Path
			if route != "" {
				span.SetName(c.Method() + " " + route)
				span.SetAttributes(attribute.String("http.route", route))
			}
			if watchtower.CaptureActive() {
				capturePayload(c, span, route)
			}
			span.SetAttributes(attribute.Int("http.response.status_code", status))
			if status >= http.StatusInternalServerError {
				span.SetStatus(codes.Error, http.StatusText(status))
			}
		}()
		return c.Next()
	}
}

// capturePayload records the bodies per the rule matching route. A streamed body is skipped:
// reading it here would consume it.
func capturePayload(c *fiber.Ctx, span trace.Span, route string) {
	if route == "" {
		route = c.Path()
	}
	var req, res []byte
	if !c.Request().IsBodyStream() {
		req = head(c.Body())
	}
	if !c.Response().IsBodyStream() {
		res = head(c.Response().Body())
	}
	watchtower.CapturePayload(span, route, string(c.Request().Header.ContentType()), req,
		string(c.Response().Header.ContentType()), res)
}

func head(b []byte) []byte { return b[:min(len(b), 2*watchtower.PayloadMax)] }
