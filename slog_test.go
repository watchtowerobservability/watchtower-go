package watchtower

import (
	"context"
	"log/slog"
	"strings"
	"testing"
)

type capHandler struct{ got *[]slog.Attr }

func (capHandler) Enabled(context.Context, slog.Level) bool { return true }
func (c capHandler) Handle(_ context.Context, r slog.Record) error {
	r.Attrs(func(a slog.Attr) bool { *c.got = append(*c.got, a); return true })
	return nil
}
func (c capHandler) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c capHandler) WithGroup(string) slog.Handler      { return c }

func TestWithSourceStampsCaller(t *testing.T) {
	var got []slog.Attr
	slog.New(withSource{capHandler{&got}}).Info("hi")
	m := map[string]string{}
	for _, a := range got {
		m[a.Key] = a.Value.String()
	}
	if !strings.HasSuffix(m["code.filepath"], "slog_test.go") || m["code.lineno"] == "" {
		t.Fatalf("attrs %v", m)
	}
}
