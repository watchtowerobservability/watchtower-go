package watchtower

// App and API Protection in-app WAF (W52). StartAppsec polls the project's ruleset from the edge
// (GET /v1/appsec/ruleset, an ingest key with scope probes:read, ETag, every poll_s): mode
// (monitor or block), the enabled pattern rules and the denylist. AppsecMiddleware (also inside
// wtnethttp.Handler) inspects each request before the handler: path, query, User-Agent and the
// other header values, percent-decoded once, against the rules the worker runs over stored
// spans. A match is recorded on the request's span (appsec.event, appsec.rule_id,
// appsec.category, appsec.blocked); in block mode, and for denylisted IPs or users in either
// mode, the request gets 403 problem+json and the handler never runs. With no ruleset loaded
// every request passes. Patterns are compiled case-insensitively; one that does not compile is
// skipped.

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// AppsecDenylistRule is the rule id recorded when a denylisted caller is blocked.
const AppsecDenylistRule = "denylist"

// AppsecRule is one pattern rule as the edge serves it.
type AppsecRule struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Category string   `json:"category"`
	Severity string   `json:"severity"`
	Fields   []string `json:"fields"`
	Pattern  string   `json:"pattern"`
}

// AppsecDenyEntry is one denylisted value; ExpiresAt is unix seconds (nil: no expiry).
type AppsecDenyEntry struct {
	Value     string `json:"value"`
	ExpiresAt *int64 `json:"expires_at"`
}

// AppsecRuleset is the edge's ruleset document.
type AppsecRuleset struct {
	Mode     string       `json:"mode"`
	Rules    []AppsecRule `json:"rules"`
	Denylist struct {
		IP   []AppsecDenyEntry `json:"ip"`
		User []AppsecDenyEntry `json:"user"`
	} `json:"denylist"`
	PollS int `json:"poll_s"`
}

// AppsecRequest is one request as the inspector sees it (decoded).
type AppsecRequest struct {
	Path, Query, UserAgent, Body, Headers, IP, User string
}

// AppsecDecision: Action is "allow", "monitor" (matched, let through) or "block" (answer 403).
type AppsecDecision struct {
	Action, RuleID, Category, Match string
}

type appsecRule struct {
	rule AppsecRule
	re   *regexp.Regexp
}

type appsecCompiled struct {
	block    bool
	rules    []appsecRule
	ip, user map[string]int64
}

var appsecState atomic.Pointer[appsecCompiled]

func appsecExpiry(list []AppsecDenyEntry) map[string]int64 {
	m := make(map[string]int64, len(list))
	for _, e := range list {
		exp := int64(1<<62 - 1)
		if e.ExpiresAt != nil {
			exp = *e.ExpiresAt
		}
		m[e.Value] = exp
	}
	return m
}

// SetAppsecRuleset replaces the active ruleset (nil clears it).
func SetAppsecRuleset(r *AppsecRuleset) {
	if r == nil {
		appsecState.Store(nil)
		return
	}
	c := &appsecCompiled{block: r.Mode == "block", ip: appsecExpiry(r.Denylist.IP), user: appsecExpiry(r.Denylist.User)}
	for _, rule := range r.Rules {
		if re, err := regexp.Compile("(?i)" + rule.Pattern); err == nil {
			c.rules = append(c.rules, appsecRule{rule, re})
		}
	}
	appsecState.Store(c)
}

func (c *appsecCompiled) listed(m map[string]int64, v string, now time.Time) bool {
	exp, ok := m[v]
	return v != "" && ok && exp > now.Unix()
}

// AppsecUserBlocked reports whether id is denylisted (call after authentication).
func AppsecUserBlocked(id string) bool {
	c := appsecState.Load()
	return c != nil && c.listed(c.user, id, time.Now())
}

func (r AppsecRequest) field(f string) string {
	switch f {
	case "path":
		return r.Path
	case "query":
		return r.Query
	case "user_agent":
		return r.UserAgent
	case "body":
		return r.Body
	case "headers":
		return r.Headers
	}
	return ""
}

// AppsecInspect judges one request. Denylisted callers are blocked in either mode.
func AppsecInspect(req AppsecRequest, now time.Time) AppsecDecision {
	c := appsecState.Load()
	if c == nil {
		return AppsecDecision{Action: "allow"}
	}
	if c.listed(c.ip, req.IP, now) || c.listed(c.user, req.User, now) {
		return AppsecDecision{Action: "block", RuleID: AppsecDenylistRule, Category: "abuse"}
	}
	for _, r := range c.rules {
		for _, f := range r.rule.Fields {
			v := req.field(f)
			if v == "" {
				continue
			}
			if loc := r.re.FindStringIndex(v); loc != nil {
				m := v[loc[0]:loc[1]]
				action := "monitor"
				if c.block {
					action = "block"
				}
				if len(m) > 200 {
					m = m[:200]
				}
				return AppsecDecision{Action: action, RuleID: r.rule.ID, Category: r.rule.Category, Match: m}
			}
		}
	}
	return AppsecDecision{Action: "allow"}
}

// AppsecClientIP is the first X-Forwarded-For hop, else X-Real-IP, else the peer address.
// shortcut: trusts the forwarding headers; run behind a proxy that overwrites them.
func AppsecClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		first, _, _ := strings.Cut(xff, ",")
		return strings.TrimSpace(first)
	}
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// AppsecRequestFrom decodes r into the inspector's fields (the body is not read).
func AppsecRequestFrom(r *http.Request, user string) AppsecRequest {
	query, err := url.QueryUnescape(r.URL.RawQuery)
	if err != nil {
		query = r.URL.RawQuery
	}
	var hs []string
	for k, v := range r.Header {
		if k != "User-Agent" {
			hs = append(hs, strings.Join(v, ", "))
		}
	}
	return AppsecRequest{
		Path: r.URL.Path, Query: query, UserAgent: r.UserAgent(), Headers: strings.Join(hs, "\n"),
		IP: AppsecClientIP(r), User: user,
	}
}

func appsecAnnotate(ctx context.Context, d AppsecDecision, ip string) {
	span := trace.SpanFromContext(ctx)
	if ip != "" {
		span.SetAttributes(attribute.String("appsec.client_ip", ip))
	}
	if d.Action == "allow" {
		return
	}
	span.SetAttributes(attribute.Bool("appsec.event", true), attribute.String("appsec.rule_id", d.RuleID),
		attribute.String("appsec.category", d.Category), attribute.Bool("appsec.blocked", d.Action == "block"))
}

const appsecProblem = `{"type":"about:blank","title":"Forbidden","status":403,"detail":"Request blocked by Watchtower App and API Protection"}`

// AppsecMiddleware inspects each request before next and answers 403 when it is blocked. Put it
// inside the server span (wtnethttp.Handler does).
func AppsecMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if appsecState.Load() == nil {
			next.ServeHTTP(w, r)
			return
		}
		req := AppsecRequestFrom(r, "")
		d := AppsecInspect(req, time.Now())
		appsecAnnotate(r.Context(), d, req.IP)
		if d.Action == "block" {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, appsecProblem)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// TrackLogin marks the current request as a login attempt; failed ones feed the
// account-takeover rules.
func TrackLogin(ctx context.Context, login string, success bool) {
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(attribute.String("appsec.login", login), attribute.Bool("appsec.login.success", success))
	if success {
		span.SetAttributes(attribute.Bool("appsec.auth", true))
	}
}

// fetchAppsec does one poll; it returns nil when unchanged or on failure.
func fetchAppsec(ctx context.Context, client *http.Client, apiURL, key, service, etag string) (*AppsecRuleset, string) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL+"/v1/appsec/ruleset?service="+url.QueryEscape(service), nil)
	if err != nil {
		return nil, etag
	}
	req.Header.Set("Authorization", "Bearer "+key)
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, etag
	}
	defer resp.Body.Close()
	var body AppsecRuleset
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&body) != nil {
		return nil, etag
	}
	return &body, resp.Header.Get("ETag")
}

// StartAppsec fetches the ruleset now and keeps polling until ctx is done.
func StartAppsec(ctx context.Context, client *http.Client, apiURL, key, service string) {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	apiURL = strings.TrimRight(apiURL, "/")
	poll := func(etag string) (string, time.Duration) {
		r, tag := fetchAppsec(ctx, client, apiURL, key, service, etag)
		wait := 30 * time.Second
		if r != nil {
			SetAppsecRuleset(r)
			if r.PollS > 10 {
				wait = time.Duration(r.PollS) * time.Second
			}
		}
		return tag, wait
	}
	etag, wait := poll("")
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
				etag, wait = poll(etag)
			}
		}
	}()
}
