package watchtower

import (
	"context"
	"runtime"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// codeOrigin stamps the file, line and function that started a span as the OTel semconv code.*
// attributes (L2). Opt-in (CodeOrigin, WT_CODE_ORIGIN=1): it walks the stack per span. Frames in
// the OpenTelemetry packages, this SDK's own packages and the standard library are skipped.
type codeOrigin struct{}

var originSkip = []string{"go.opentelemetry.io/", "github.com/watchtowerobservability/watchtower-go", "runtime.", "net/http.", "testing."}

// appFrame is the first frame of pcs outside the skipped packages.
func appFrame(pcs []uintptr) (runtime.Frame, bool) {
	frames := runtime.CallersFrames(pcs)
	for {
		f, more := frames.Next()
		skip := false
		for _, p := range originSkip {
			if strings.HasPrefix(f.Function, p) && !strings.HasSuffix(f.File, "_test.go") {
				skip = true
				break
			}
		}
		if !skip && f.Function != "" {
			return f, true
		}
		if !more {
			return runtime.Frame{}, false
		}
	}
}

func (codeOrigin) OnStart(_ context.Context, s sdktrace.ReadWriteSpan) {
	for _, a := range s.Attributes() {
		if a.Key == "code.filepath" {
			return
		}
	}
	var pcs [32]uintptr
	n := runtime.Callers(2, pcs[:])
	if f, ok := appFrame(pcs[:n]); ok {
		s.SetAttributes(attribute.String("code.filepath", f.File), attribute.Int("code.lineno", f.Line),
			attribute.String("code.function", f.Function))
	}
}

func (codeOrigin) OnEnd(sdktrace.ReadOnlySpan)      {}
func (codeOrigin) Shutdown(context.Context) error   { return nil }
func (codeOrigin) ForceFlush(context.Context) error { return nil }
