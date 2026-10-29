// Package wtchi instruments go-chi routers: server spans named after the matched route pattern
// plus panic capture.
package wtchi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	wtnethttp "github.com/watchtowerobservability/watchtower-go/nethttp"
)

// Middleware is for r.Use. The span is renamed to "METHOD /route/{pattern}" once chi has routed.
func Middleware(next http.Handler) http.Handler {
	named := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		if rc := chi.RouteContext(r.Context()); rc != nil {
			if p := rc.RoutePattern(); p != "" {
				s := trace.SpanFromContext(r.Context())
				s.SetName(r.Method + " " + p)
				s.SetAttributes(attribute.String("http.route", p))
			}
		}
	})
	return otelhttp.NewHandler(wtnethttp.Recover(named), "http.request",
		// otelhttp renames the span again after routing when chi sets r.Pattern, so the formatter
		// must produce the route name too, not just the method.
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
			if rc := chi.RouteContext(r.Context()); rc != nil {
				if p := rc.RoutePattern(); p != "" {
					return r.Method + " " + p
				}
			}
			return r.Method
		}))
}
