package watchtower

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/contrib/bridges/otelslog"
)

// SlogHandler returns a log/slog handler that exports records to Watchtower as OTLP logs, with
// the trace and span ids of the context passed to the *Context logging methods. When next is
// not nil every record also goes to next (for example slog.NewJSONHandler(os.Stderr, nil)).
// Call it after Init so the global logger provider is the Watchtower one.
//
//	slog.SetDefault(slog.New(watchtower.SlogHandler(slog.NewTextHandler(os.Stderr, nil))))
func SlogHandler(next slog.Handler) slog.Handler {
	h := otelslog.NewHandler("github.com/watchtowerobservability/watchtower-go")
	if next == nil {
		return h
	}
	return fanout{h, next}
}

type fanout [2]slog.Handler

func (f fanout) Enabled(ctx context.Context, l slog.Level) bool {
	return f[0].Enabled(ctx, l) || f[1].Enabled(ctx, l)
}

func (f fanout) Handle(ctx context.Context, r slog.Record) error {
	var first error
	for _, h := range f {
		if h.Enabled(ctx, r.Level) {
			if err := h.Handle(ctx, r.Clone()); err != nil && first == nil {
				first = err
			}
		}
	}
	return first
}

func (f fanout) WithAttrs(a []slog.Attr) slog.Handler { return fanout{f[0].WithAttrs(a), f[1].WithAttrs(a)} }
func (f fanout) WithGroup(n string) slog.Handler      { return fanout{f[0].WithGroup(n), f[1].WithGroup(n)} }
