// Package wtgin instruments Gin: server spans, panic capture, capture of c.Errors and payload
// capture per the capture rules.
package wtgin

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
	"go.opentelemetry.io/otel/trace"

	watchtower "github.com/watchtowerobservability/watchtower-go"
	wtnethttp "github.com/watchtowerobservability/watchtower-go/nethttp"
)

// Use installs tracing and recovery on r (an *gin.Engine or group).
func Use(r gin.IRoutes, service string) {
	r.Use(otelgin.Middleware(service), Recovery(), Capture())
}

// Recovery captures panics and errors added with c.Error on the request span. It must run
// after the otelgin middleware (Use does that).
func Recovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if rec := recover(); rec != nil {
				watchtower.CapturePanic(c.Request.Context(), rec)
				c.AbortWithStatus(http.StatusInternalServerError)
			}
		}()
		c.Next()
		for _, e := range c.Errors {
			watchtower.CaptureError(c.Request.Context(), e.Err)
		}
	}
}

type tapWriter struct {
	gin.ResponseWriter
	buf *wtnethttp.CapBuf
}

func (w tapWriter) Write(p []byte) (int, error) {
	_, _ = w.buf.Write(p)
	return w.ResponseWriter.Write(p)
}

func (w tapWriter) WriteString(s string) (int, error) {
	_, _ = w.buf.Write([]byte(s))
	return w.ResponseWriter.WriteString(s)
}

// Capture records the request and response bodies on the request span per the capture rule
// matching the route (watchtower.CapturePayload); it is a pass-through when no rules are loaded.
// It must run after the otelgin middleware (Use does that).
func Capture() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !watchtower.CaptureActive() {
			c.Next()
			return
		}
		req, res := wtnethttp.TapRequest(c.Request), &wtnethttp.CapBuf{}
		c.Writer = tapWriter{c.Writer, res}
		c.Next()
		route := c.FullPath()
		if route == "" {
			route = c.Request.URL.Path
		}
		watchtower.CapturePayload(trace.SpanFromContext(c.Request.Context()), route,
			c.GetHeader("Content-Type"), req.Bytes(), c.Writer.Header().Get("Content-Type"), res.Bytes())
	}
}
