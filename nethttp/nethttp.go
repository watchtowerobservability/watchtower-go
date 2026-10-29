// Package wtnethttp instruments net/http servers and clients: a server span per request and
// panic capture with stack and trace ids.
package wtnethttp

import (
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	watchtower "github.com/watchtowerobservability/watchtower-go"
)

// Recover captures a handler panic on the request's span and answers 500. Put it inside Handler
// (Handler does) so the span exists. http.ErrAbortHandler is passed through untouched.
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				if rec == http.ErrAbortHandler {
					panic(rec)
				}
				watchtower.CapturePanic(r.Context(), rec)
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// Handler wraps next with a server span named operation and panic capture.
func Handler(next http.Handler, operation string, opts ...otelhttp.Option) http.Handler {
	return otelhttp.NewHandler(Recover(next), operation, opts...)
}

// Transport wraps rt (nil means http.DefaultTransport) with client spans and trace propagation.
func Transport(rt http.RoundTripper) http.RoundTripper { return otelhttp.NewTransport(rt) }
