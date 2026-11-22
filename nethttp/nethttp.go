// Package wtnethttp instruments net/http servers and clients: a server span per request and
// panic capture with stack and trace ids.
package wtnethttp

import (
	"bytes"
	"io"
	"net/http"
	"strings"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/trace"

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

// Handler wraps next with a server span named operation, panic capture, the in-app WAF
// (watchtower.AppsecMiddleware, a no-op until StartAppsec loads a ruleset) and payload capture
// (watchtower.CapturePayload, on only when capture rules are loaded).
func Handler(next http.Handler, operation string, opts ...otelhttp.Option) http.Handler {
	return otelhttp.NewHandler(Recover(watchtower.AppsecMiddleware(Capture(next))), operation, opts...)
}

// CapBuf keeps the first 2*PayloadMax bytes written to it.
type CapBuf struct{ bytes.Buffer }

func (b *CapBuf) Write(p []byte) (int, error) {
	if room := 2*watchtower.PayloadMax - b.Len(); room > 0 {
		b.Buffer.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

type tapReader struct {
	io.ReadCloser
	tee io.Reader
}

func (t tapReader) Read(p []byte) (int, error) { return t.tee.Read(p) }

type tapWriter struct {
	http.ResponseWriter
	buf *CapBuf
}

func (w tapWriter) Write(p []byte) (int, error) {
	_, _ = w.buf.Write(p)
	return w.ResponseWriter.Write(p)
}

// Unwrap lets http.ResponseController reach Flush, Hijack and deadlines.
func (w tapWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// TapRequest copies what the handler reads from r.Body into the returned buffer; it never reads
// more than the handler does. For framework integrations with their own context types.
func TapRequest(r *http.Request) *CapBuf {
	b := &CapBuf{}
	if r.Body != nil {
		r.Body = tapReader{r.Body, io.TeeReader(r.Body, b)}
	}
	return b
}

// TapResponse wraps w so the first bytes the handler writes are also kept in the returned buffer.
func TapResponse(w http.ResponseWriter) (http.ResponseWriter, *CapBuf) {
	b := &CapBuf{}
	return tapWriter{w, b}, b
}

// Capture records the request and response bodies the handler reads and writes on the request's
// span, per the capture rule matching the route (r.Pattern, else the path). It never reads more
// of the body than the handler does.
func Capture(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !watchtower.CaptureActive() {
			next.ServeHTTP(w, r)
			return
		}
		req := TapRequest(r)
		tw, res := TapResponse(w)
		next.ServeHTTP(tw, r)
		route := r.Pattern
		if _, path, ok := strings.Cut(route, " "); ok {
			route = path // "GET /items/{id}" patterns carry the method
		}
		if route == "" {
			route = r.URL.Path
		}
		watchtower.CapturePayload(trace.SpanFromContext(r.Context()), route,
			r.Header.Get("Content-Type"), req.Bytes(), w.Header().Get("Content-Type"), res.Bytes())
	})
}

// Transport wraps rt (nil means http.DefaultTransport) with client spans and trace propagation.
func Transport(rt http.RoundTripper) http.RoundTripper { return otelhttp.NewTransport(rt) }
