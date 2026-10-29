// Package wtgin instruments Gin: server spans, panic capture and capture of c.Errors.
package wtgin

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"

	watchtower "github.com/watchtowerobservability/watchtower-go"
)

// Use installs tracing and recovery on r (an *gin.Engine or group).
func Use(r gin.IRoutes, service string) {
	r.Use(otelgin.Middleware(service), Recovery())
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
