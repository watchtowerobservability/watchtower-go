package watchtower

// Payload and GraphQL capture (APM-016), driven by the capture rules set in Watchtower per
// service and route (GET /v1/capture-rules?service=, an ingest key with scope probes:read).
// Opt-in: nothing is fetched or recorded unless Options.CaptureRules or WT_CAPTURE_RULES=1, and
// a rule matches the route. Only text-like bodies are kept, at most the rule's body_bytes (hard
// cap 8 KiB), after the default secret and PII detectors (a port of the pipeline's redact
// stage; ingest applies the project's detectors again). wtnethttp.Handler records them.
// shortcut: rules are fetched once at Init; poll with the ETag (poll_s) when live edits matter.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// CaptureRule is one payload capture rule as the edge serves it.
type CaptureRule struct {
	Route        string `json:"route"`
	BodyBytes    int    `json:"body_bytes"`
	GraphQLQuery bool   `json:"graphql_query"`
}

// PayloadMax is the hard cap on a captured body excerpt.
const PayloadMax = 8192

var captureRules atomic.Pointer[[]CaptureRule]

// SetCaptureRules replaces the active rules (nil turns capture off).
func SetCaptureRules(r []CaptureRule) { captureRules.Store(&r) }

// CaptureActive reports whether any rule is loaded; integrations skip body taps otherwise.
func CaptureActive() bool {
	r := captureRules.Load()
	return r != nil && len(*r) > 0
}

// LoadCaptureRules fetches the service's rules from apiURL; any failure leaves capture off.
func LoadCaptureRules(ctx context.Context, client *http.Client, apiURL, key, service string) []CaptureRule {
	SetCaptureRules(nil)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL+"/v1/capture-rules?service="+url.QueryEscape(service), nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	var body struct {
		Rules []CaptureRule `json:"rules"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body) != nil {
		return nil
	}
	SetCaptureRules(body.Rules)
	return body.Rules
}

// glob: "*" matches any run of characters (the edge's glob).
func glob(pat, s string) bool {
	head, tail, ok := strings.Cut(pat, "*")
	if !ok {
		return pat == s
	}
	rest, found := strings.CutPrefix(s, head)
	if !found {
		return false
	}
	for i := 0; i <= len(rest); i++ {
		if glob(tail, rest[i:]) {
			return true
		}
	}
	return false
}

func ruleFor(route string) (CaptureRule, bool) {
	if r := captureRules.Load(); r != nil {
		for _, x := range *r {
			if x.Route == "" || glob(x.Route, route) {
				return x, true
			}
		}
	}
	return CaptureRule{}, false
}

const secretKeys = `password|passwd|secret|token|access_token|refresh_token|client_secret|api_?key|authorization`

var jsonSecret = regexp.MustCompile(`(?i)("(?:` + secretKeys + `)"\s*:\s*)"(?:[^"\\]|\\.)*"`)

var formSecret = regexp.MustCompile(`(?i)\b((?:` + secretKeys + `)=)[^&\s]*`)

var piiPatterns = []*regexp.Regexp{
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`),
	regexp.MustCompile(`eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`),
	regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._~+/-]+=*`),
	regexp.MustCompile(`wt[ik]_[a-z0-9]{8}_[A-Za-z0-9_-]{43}`),
	regexp.MustCompile(`(?:AKIA|ASIA)[0-9A-Z]{16}`),
	regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{36,}`),
	regexp.MustCompile(`[rs]k_(?:live|test)_[A-Za-z0-9]{16,}`),
	regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,}`),
	regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`),
}

var cardRe = regexp.MustCompile(`\b\d{4}[ -]?\d{4}[ -]?\d{4}[ -]?\d{1,7}\b`)

var gqlRe = regexp.MustCompile(`(?:^|\})\s*(query|mutation|subscription)\b\s*([_A-Za-z][_0-9A-Za-z]*)?`)

var gqlNote = regexp.MustCompile(`#[^\n]*`)

var textType = regexp.MustCompile(`(?i)json|^text/|xml|x-www-form-urlencoded|graphql`)

const redacted = "[REDACTED]"

func luhn(s string) bool {
	sum, n := 0, 0
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] < '0' || s[i] > '9' {
			continue
		}
		x := int(s[i] - '0')
		if n%2 == 1 {
			if x *= 2; x > 9 {
				x -= 9
			}
		}
		sum += x
		n++
	}
	return n >= 13 && sum%10 == 0
}

// RedactText applies the default secret and PII detectors: secret-named JSON and form fields,
// keys, tokens, emails, SSNs and Luhn-valid card numbers.
func RedactText(s string) string {
	s = jsonSecret.ReplaceAllString(s, `${1}"`+redacted+`"`)
	s = formSecret.ReplaceAllString(s, `${1}`+redacted)
	for _, re := range piiPatterns {
		s = re.ReplaceAllLiteralString(s, redacted)
	}
	return cardRe.ReplaceAllStringFunc(s, func(m string) string {
		if luhn(m) {
			return redacted
		}
		return m
	})
}

func capBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

// excerpt redacts a wider window than it keeps so a secret cut at the edge is still masked.
func excerpt(s string, n int) string { return capBytes(RedactText(capBytes(s, n*2)), n) }

// GraphQLOp returns the operation type and name of a document ("{ ... }" is an anonymous query).
func GraphQLOp(doc string) (typ, name string, ok bool) {
	d := gqlNote.ReplaceAllString(doc, "")
	if m := gqlRe.FindStringSubmatch(d); m != nil {
		return m[1], m[2], true
	}
	if strings.HasPrefix(strings.TrimSpace(d), "{") {
		return "query", "", true
	}
	return "", "", false
}

// CapturePayload records the bodies and GraphQL operation the rule matching route asks for on
// span; without a matching rule it records nothing. Non-text content types are skipped.
func CapturePayload(span trace.Span, route, reqType string, reqBody []byte, resType string, resBody []byte) {
	rule, ok := ruleFor(route)
	if !ok || span == nil || !span.IsRecording() {
		return
	}
	n := min(rule.BodyBytes, PayloadMax)
	text := func(ct string, b []byte) string {
		if len(b) == 0 || (ct != "" && !textType.MatchString(ct)) {
			return ""
		}
		return string(b)
	}
	req, res := text(reqType, reqBody), text(resType, resBody)
	if n > 0 && req != "" {
		span.SetAttributes(attribute.String("http.request.body", excerpt(req, n)))
	}
	if n > 0 && res != "" {
		span.SetAttributes(attribute.String("http.response.body", excerpt(res, n)))
	}
	doc, opName := "", ""
	if req != "" && strings.Contains(strings.ToLower(reqType), "graphql") {
		doc = req
	} else if req != "" {
		var j struct {
			Query         *string `json:"query"`
			OperationName *string `json:"operationName"`
		}
		if json.Unmarshal([]byte(req), &j) == nil && j.Query != nil {
			doc = *j.Query
			if j.OperationName != nil {
				opName = *j.OperationName
			}
		}
	}
	if doc == "" {
		return
	}
	typ, name, ok := GraphQLOp(doc)
	if !ok {
		return
	}
	span.SetAttributes(attribute.String("graphql.operation.type", typ))
	if opName != "" {
		name = opName
	}
	if name != "" {
		span.SetAttributes(attribute.String("graphql.operation.name", name))
	}
	if rule.GraphQLQuery {
		span.SetAttributes(attribute.String("graphql.document", excerpt(doc, PayloadMax)))
	}
}
