package watchtower

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func appsecFixture(mode string) *AppsecRuleset {
	past := int64(1)
	r := &AppsecRuleset{Mode: mode, Rules: []AppsecRule{
		{ID: "sqli-union", Category: "sqli", Fields: []string{"query", "body", "path"}, Pattern: `\bunion\b(?:\s+all)?\s+(?:distinct\s+)?select\b`},
		{ID: "scanner-ua", Category: "scanner", Fields: []string{"user_agent"}, Pattern: `sqlmap|nikto`},
		{ID: "broken", Category: "custom", Fields: []string{"path"}, Pattern: `(`},
	}}
	r.Denylist.IP = []AppsecDenyEntry{{Value: "203.0.113.9"}}
	r.Denylist.User = []AppsecDenyEntry{{Value: "mallory", ExpiresAt: &past}}
	return r
}

func TestAppsecInspectDecodesAndMatches(t *testing.T) {
	defer SetAppsecRuleset(nil)
	SetAppsecRuleset(appsecFixture("monitor"))
	r := httptest.NewRequest(http.MethodGet, "/search?q=1%20UNION+SELECT%20pw", nil)
	r.Header.Set("X-Forwarded-For", "198.51.100.1, 10.0.0.1")
	req := AppsecRequestFrom(r, "")
	if req.Query != "q=1 UNION SELECT pw" || req.IP != "198.51.100.1" {
		t.Fatalf("decoded %+v", req)
	}
	if d := AppsecInspect(req, time.Now()); d.Action != "monitor" || d.RuleID != "sqli-union" {
		t.Fatalf("decision %+v", d)
	}
	if d := AppsecInspect(AppsecRequest{Path: "/", UserAgent: "sqlmap/1.7"}, time.Now()); d.RuleID != "scanner-ua" {
		t.Fatalf("ua decision %+v", d)
	}
	if d := AppsecInspect(AppsecRequest{Path: "/books", Query: "q=union station"}, time.Now()); d.Action != "allow" {
		t.Fatalf("benign decision %+v", d)
	}
	if AppsecUserBlocked("mallory") {
		t.Fatal("expired entry still blocks")
	}
}

func TestAppsecMiddlewareBlocks(t *testing.T) {
	defer SetAppsecRuleset(nil)
	called := false
	h := AppsecMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { called = true }))
	SetAppsecRuleset(appsecFixture("block"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/?q=1+union+select+2", nil))
	if w.Code != http.StatusForbidden || called || w.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("block mode: code %d called %v", w.Code, called)
	}
	SetAppsecRuleset(appsecFixture("monitor"))
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/?q=1+union+select+2", nil))
	if !called {
		t.Fatal("monitor mode must let the request through")
	}
	called = false
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "203.0.113.9:4000"
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden || called {
		t.Fatal("denylisted IP must be blocked in monitor mode")
	}
}

func TestStartAppsecPollsWithKey(t *testing.T) {
	defer SetAppsecRuleset(nil)
	var auth, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth, path = r.Header.Get("Authorization"), r.URL.String()
		w.Header().Set("ETag", `"abc"`)
		_, _ = w.Write([]byte(`{"mode":"block","rules":[],"denylist":{"ip":[{"value":"192.0.2.1"}],"user":[]},"poll_s":30}`))
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	StartAppsec(ctx, srv.Client(), srv.URL+"/", "wti_x", "api")
	if auth != "Bearer wti_x" || path != "/v1/appsec/ruleset?service=api" {
		t.Fatalf("auth %q path %q", auth, path)
	}
	if d := AppsecInspect(AppsecRequest{Path: "/", IP: "192.0.2.1"}, time.Now()); d.Action != "block" {
		t.Fatalf("denylist not loaded: %+v", d)
	}
}
