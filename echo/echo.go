// Package wtecho instruments Echo v4: server spans, panic capture, capture of handler errors and
// payload capture per the capture rules.
package wtecho

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"
	"go.opentelemetry.io/contrib/instrumentation/github.com/labstack/echo/otelecho"
	"go.opentelemetry.io/otel/trace"

	watchtower "github.com/watchtowerobservability/watchtower-go"
	wtnethttp "github.com/watchtowerobservability/watchtower-go/nethttp"
)

// Use installs tracing and recovery on e.
func Use(e *echo.Echo, service string) {
	e.Use(otelecho.Middleware(service), Recovery(), Capture())
}

// Recovery captures panics and server-side handler errors (non-HTTP errors and HTTP 5xx) on the
// request span. It must run after the otelecho middleware (Use does that).
func Recovery() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) (err error) {
			ctx := c.Request().Context()
			defer func() {
				if rec := recover(); rec != nil {
					watchtower.CapturePanic(ctx, rec)
					err = echo.ErrInternalServerError
				}
			}()
			err = next(c)
			var he *echo.HTTPError
			if err != nil && (!errors.As(err, &he) || he.Code >= http.StatusInternalServerError) {
				watchtower.CaptureError(ctx, err)
			}
			return err
		}
	}
}

// Capture records the request and response bodies on the request span per the capture rule
// matching the route (watchtower.CapturePayload); it is a pass-through when no rules are loaded.
// A body that Echo's error handler writes after the chain returns is not seen.
func Capture() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if !watchtower.CaptureActive() {
				return next(c)
			}
			req := wtnethttp.TapRequest(c.Request())
			w, res := wtnethttp.TapResponse(c.Response().Writer)
			c.Response().Writer = w
			err := next(c)
			route := c.Path()
			if route == "" {
				route = c.Request().URL.Path
			}
			watchtower.CapturePayload(trace.SpanFromContext(c.Request().Context()), route,
				c.Request().Header.Get("Content-Type"), req.Bytes(), c.Response().Header().Get("Content-Type"), res.Bytes())
			return err
		}
	}
}
