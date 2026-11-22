package watchtower

import (
	"context"
	"encoding/json"
	"hash/fnv"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// remoteConfig is GET /v1/sampling: a PROBABILISTIC rate or the project's head rules.
type remoteConfig struct {
	StrategyType  string `json:"strategyType"`
	Probabilistic *struct {
		SamplingRate float64 `json:"samplingRate"`
	} `json:"probabilisticSampling"`
	Rules []remoteRule `json:"rules"`
}

type remoteRule struct {
	Enabled *bool `json:"enabled"`
	Match   struct {
		Service       string            `json:"service"`
		Env           string            `json:"env"`
		NamePrefix    string            `json:"span_name_prefix"`
		NameGlob      string            `json:"span_name_glob"`
		NameRegex     string            `json:"span_name_regex"`
		Attrs         map[string]string `json:"attrs"`
		MinDurationMs *uint64           `json:"min_duration_ms"`
		Status        string            `json:"status"`
	} `json:"match"`
	Action struct {
		Kind  string   `json:"kind"`
		Ratio *float64 `json:"ratio"`
	} `json:"action"`
}

func clamp01(r float64) float64 { return min(1, max(0, r)) }

// ratioFor is the keep ratio for a root span (1 keeps). The first matching ratio or drop rule
// wins; rules that need duration or status are left to the gateway.
func (c *remoteConfig) ratioFor(service, env, name string, attrs []attribute.KeyValue) float64 {
	if c.StrategyType == "PROBABILISTIC" && c.Probabilistic != nil {
		return clamp01(c.Probabilistic.SamplingRate)
	}
	for i := range c.Rules {
		r := &c.Rules[i]
		m := &r.Match
		if (r.Enabled != nil && !*r.Enabled) || m.MinDurationMs != nil || m.Status != "" ||
			(m.Service != "" && m.Service != service) || (m.Env != "" && m.Env != env) || !strings.HasPrefix(name, m.NamePrefix) {
			continue
		}
		if m.NameGlob != "" && !globMatch(m.NameGlob, name) {
			continue
		}
		if m.NameRegex != "" {
			if re, err := regexp.Compile(m.NameRegex); err != nil || !re.MatchString(name) {
				continue
			}
		}
		if !attrsMatch(m.Attrs, attrs) {
			continue
		}
		switch r.Action.Kind {
		case "drop":
			return 0
		case "ratio":
			if r.Action.Ratio != nil {
				return clamp01(*r.Action.Ratio)
			}
		}
		return 1
	}
	return 1
}

func attrsMatch(want map[string]string, have []attribute.KeyValue) bool {
	for k, v := range want {
		found := false
		for _, kv := range have {
			if string(kv.Key) == k && kv.Value.Emit() == v {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// RemoteSampler applies the project's head sampling rules in the process. It polls
// GET /v1/sampling with the ETag every interval; a failed poll keeps the last good config
// (initially: keep everything). It decides by fnv64a of the trace id like the gateway, so every
// service agrees on a trace. Wrap it in sdktrace.ParentBased. Spans dropped here never reach
// the gateway, so its exact request counts see only what is kept.
type RemoteSampler struct {
	cfg                atomic.Pointer[remoteConfig]
	endpoint, key      string
	service, env, etag string
	client             *http.Client
	stop               chan struct{}
	once               sync.Once
}

// NewRemoteSampler starts polling at once; Stop ends it.
func NewRemoteSampler(endpoint, key, service, env string, interval time.Duration) *RemoteSampler {
	s := &RemoteSampler{endpoint: strings.TrimRight(endpoint, "/"), key: key, service: service, env: env,
		client: &http.Client{Timeout: 10 * time.Second}, stop: make(chan struct{})}
	s.cfg.Store(&remoteConfig{})
	if interval < time.Second {
		interval = 30 * time.Second
	}
	go func() {
		s.Poll(context.Background())
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-s.stop:
				return
			case <-t.C:
				s.Poll(context.Background())
			}
		}
	}()
	return s
}

// Stop ends the polling.
func (s *RemoteSampler) Stop() { s.once.Do(func() { close(s.stop) }) }

// Poll fetches the config once; any failure or a 304 leaves the current one.
func (s *RemoteSampler) Poll(ctx context.Context) {
	q := url.Values{"service": {s.service}}
	if s.env != "" {
		q.Set("env", s.env)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.endpoint+"/v1/sampling?"+q.Encode(), nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+s.key)
	if s.etag != "" {
		req.Header.Set("If-None-Match", s.etag)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}
	var c remoteConfig
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&c) != nil {
		return
	}
	s.etag = resp.Header.Get("ETag")
	s.cfg.Store(&c)
}

// ShouldSample implements sdktrace.Sampler.
func (s *RemoteSampler) ShouldSample(p sdktrace.SamplingParameters) sdktrace.SamplingResult {
	r := s.cfg.Load().ratioFor(s.service, s.env, p.Name, p.Attributes)
	if r >= 1 || (r > 0 && traceFraction(p.TraceID[:]) < r) {
		return sdktrace.SamplingResult{Decision: sdktrace.RecordAndSample}
	}
	return sdktrace.SamplingResult{Decision: sdktrace.Drop}
}

// Description implements sdktrace.Sampler.
func (s *RemoteSampler) Description() string { return "WatchtowerRemoteSampler" }

// traceFraction is fnv64a(trace id) / 2^64, the gateway's ratioKeeps input.
func traceFraction(id []byte) float64 {
	h := fnv.New64a()
	_, _ = h.Write(id)
	return float64(h.Sum64()) / 18446744073709551616.0
}

// globMatch: whole-string match, `*` any run (also empty, also `/`), `?` one character.
func globMatch(glob, text string) bool {
	re := strings.NewReplacer(`\*`, ".*", `\?`, ".").Replace(regexp.QuoteMeta(glob))
	ok, _ := regexp.MatchString("(?s)^"+re+"$", text)
	return ok
}
