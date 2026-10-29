// Package wtecho instruments Echo v4: server spans, panic capture and capture of handler errors.
package wtecho

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"
	"go.opentelemetry.io/contrib/instrumentation/github.com/labstack/echo/otelecho"

	watchtower "github.com/watchtowerobservability/watchtower-go"
)

// Use installs tracing and recovery on e.
func Use(e *echo.Echo, service string) {
	e.Use(otelecho.Middleware(service), Recovery())
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
