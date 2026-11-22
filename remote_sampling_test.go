package watchtower

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func sampled(s *RemoteSampler, n int) int {
	kept := 0
	for i := 0; i < n; i++ {
		var id trace.TraceID
		id[0], id[1], id[2] = byte(i), byte(i>>8), 7
		if s.ShouldSample(sdktrace.SamplingParameters{TraceID: id, Name: "GET /x"}).Decision == sdktrace.RecordAndSample {
			kept++
		}
	}
	return kept
}

// A new ratio applies on the next poll without restart; a failing poll keeps the last config.
func TestRemoteSamplerAppliesPollsAndFallsBack(t *testing.T) {
	var rate atomic.Value
	rate.Store(`{"strategyType":"PROBABILISTIC","probabilisticSampling":{"samplingRate":1}}`)
	var down atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if down.Load() || r.Header.Get("Authorization") != "Bearer k" || r.URL.Query().Get("service") != "svc" {
			http.Error(w, "no", http.StatusInternalServerError)
			return
		}
		w.Header().Set("ETag", `"`+rate.Load().(string)[:30]+`"`)
		_, _ = w.Write([]byte(rate.Load().(string)))
	}))
	defer srv.Close()
	s := NewRemoteSampler(srv.URL, "k", "svc", "", time.Hour)
	defer s.Stop()
	s.Poll(context.Background())
	if n := sampled(s, 400); n != 400 {
		t.Fatalf("rate 1 kept %d", n)
	}
	rate.Store(`{"strategyType":"PROBABILISTIC","probabilisticSampling":{"samplingRate":0}}`)
	s.Poll(context.Background())
	if n := sampled(s, 400); n != 0 {
		t.Fatalf("rate 0 kept %d", n)
	}
	rate.Store(`{"strategyType":"PROBABILISTIC","probabilisticSampling":{"samplingRate":1}}`)
	down.Store(true)
	s.Poll(context.Background())
	if n := sampled(s, 400); n != 0 {
		t.Fatalf("a failed poll must keep the last config, kept %d", n)
	}
}

func TestRemoteConfigRules(t *testing.T) {
	r := 0.1
	c := &remoteConfig{StrategyType: "RULES"}
	var drop, ratio remoteRule
	drop.Match.Service, drop.Match.NameGlob, drop.Action.Kind = "api", "GET /health*", "drop"
	ratio.Match.Service, ratio.Action.Kind, ratio.Action.Ratio = "api", "ratio", &r
	c.Rules = []remoteRule{drop, ratio}
	if got := c.ratioFor("api", "", "GET /healthz", nil); got != 0 {
		t.Fatalf("health: %v", got)
	}
	if got := c.ratioFor("api", "", "GET /x", nil); got != 0.1 {
		t.Fatalf("api: %v", got)
	}
	if got := c.ratioFor("web", "", "GET /x", nil); got != 1 {
		t.Fatalf("web: %v", got)
	}
}
