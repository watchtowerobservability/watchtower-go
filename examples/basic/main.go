// Command basic is a minimal Watchtower-instrumented HTTP server.
//
//	WT_INGEST_KEY=... WT_SERVICE=basic go run ./examples/basic
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"

	watchtower "github.com/watchtowerobservability/watchtower-go"
	wtnethttp "github.com/watchtowerobservability/watchtower-go/nethttp"
)

func main() {
	ctx := context.Background()
	shutdown, err := watchtower.Init(ctx, watchtower.Options{})
	if err != nil {
		slog.Error("watchtower init", "err", err)
		os.Exit(1)
	}
	defer func() { _ = shutdown(ctx) }()
	slog.SetDefault(slog.New(watchtower.SlogHandler(slog.NewTextHandler(os.Stderr, nil))))

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		slog.InfoContext(r.Context(), "hello requested")
		_, _ = w.Write([]byte("hello\n"))
	})
	mux.HandleFunc("/boom", func(http.ResponseWriter, *http.Request) { panic("example panic") })
	_ = http.ListenAndServe(":8080", wtnethttp.Handler(mux, "basic"))
}
