package watchtower

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"sync/atomic"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

var redactFn atomic.Pointer[func(key, value string) string]

func setRedact(f func(key, value string) string) {
	if f == nil {
		redactFn.Store(nil)
		return
	}
	redactFn.Store(&f)
}

func redact(key, value string) string {
	if f := redactFn.Load(); f != nil {
		return (*f)(key, value)
	}
	return value
}

// CaptureError records err on the span in ctx as an OTel "exception" event with a stack trace
// and marks the span failed. Without a recording span it starts a short "error" span, so the
// error is never lost. It returns the trace id, which also appears on the correlated logs.
// A nil err is ignored. Extra attributes under "wt." are dropped; string values pass Options.Redact.
func CaptureError(ctx context.Context, err error, attrs ...attribute.KeyValue) string {
	if err == nil {
		return ""
	}
	return capture(ctx, fmt.Sprintf("%T", err), err.Error(), false, attrs)
}

// CapturePanic is CaptureError for a recovered panic value; call it from a deferred function:
//
//	defer func() {
//		if r := recover(); r != nil {
//			watchtower.CapturePanic(ctx, r)
//		}
//	}()
//
// The event carries exception.escaped=true. HTTP and gRPC middleware in the sub-packages do this for you.
func CapturePanic(ctx context.Context, v any, attrs ...attribute.KeyValue) string {
	typ := "panic"
	if e, ok := v.(error); ok {
		typ = "panic(" + fmt.Sprintf("%T", e) + ")"
	}
	return capture(ctx, typ, fmt.Sprint(v), true, attrs)
}

func capture(ctx context.Context, typ, msg string, escaped bool, attrs []attribute.KeyValue) string {
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		_, span = otel.Tracer("github.com/watchtowerobservability/watchtower-go").Start(ctx, "error")
		defer span.End()
	}
	msg = redact("exception.message", msg)
	ev := []attribute.KeyValue{
		attribute.String("exception.type", typ),
		attribute.String("exception.message", msg),
		attribute.String("exception.stacktrace", stack(4)),
		attribute.Bool("exception.escaped", escaped),
	}
	for _, kv := range safeAttrs(attrs) {
		if kv.Value.Type() == attribute.STRING {
			kv = attribute.String(string(kv.Key), redact(string(kv.Key), kv.Value.AsString()))
		}
		ev = append(ev, kv)
	}
	span.AddEvent("exception", trace.WithAttributes(ev...))
	span.SetStatus(codes.Error, msg)
	return span.SpanContext().TraceID().String()
}

// stack formats the calling goroutine's frames, skipping the first skip.
func stack(skip int) string {
	pcs := make([]uintptr, 64)
	n := runtime.Callers(skip, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	var b strings.Builder
	for {
		f, more := frames.Next()
		fmt.Fprintf(&b, "%s\n\t%s:%d\n", f.Function, f.File, f.Line)
		if !more {
			break
		}
	}
	return b.String()
}
