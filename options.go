package watchtower

import (
	"errors"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
)

// Signal selects which telemetry Init exports; combine with |.
type Signal uint8

// The signals Init can export.
const (
	Traces Signal = 1 << iota
	Metrics
	Logs
)

// DefaultEndpoint is used when neither Options.Endpoint nor WT_OTLP_ENDPOINT is set.
const DefaultEndpoint = "http://localhost:4318"

// Options configures Init. Empty fields fall back to the environment: WT_INGEST_KEY,
// WT_OTLP_ENDPOINT, WT_SERVICE, WT_ENVIRONMENT, WT_RELEASE.
type Options struct {
	Endpoint    string // OTLP/HTTP base URL
	IngestKey   string // secret ingest key, sent as "Authorization: Bearer <key>"
	ServiceName string
	Release     string // service.version
	Environment string // deployment.environment.name
	// Attributes are extra resource attributes; any key under the reserved "wt." namespace is dropped.
	Attributes []attribute.KeyValue
	// Signals zero means Traces|Metrics|Logs, minus Logs when WATCHTOWER_LOGS=off (or
	// OTEL_LOGS_EXPORTER=none) and minus Metrics when WATCHTOWER_METRICS=off (or OTEL_METRICS_EXPORTER=none).
	Signals Signal
	// BufferDir enables the offline buffer: failed exports are written there and replayed
	// oldest first after the gateway answers again (at-least-once).
	BufferDir      string
	BufferMaxBytes int64 // zero means the WAL default (512 MiB)
	// Redact, when set, is called for every captured error message and string attribute and
	// returns the value to send (return "" to drop the content).
	Redact func(key, value string) string
	// CaptureRules fetches the service's payload capture rules (APM-016) from APIURL at Init and
	// records request/response bodies and GraphQL operations where a rule matches the route
	// (wtnethttp.Handler). Also on with WT_CAPTURE_RULES=1.
	CaptureRules bool
	APIURL       string // edge base URL for capture rules; WT_API_URL, else Endpoint
	// RemoteSampling applies the project's head sampling rules in the process, polled from
	// /v1/sampling every SamplingInterval (default 30 s, WT_SAMPLING_INTERVAL_S); the last good
	// config stays in force when a poll fails. Also on with WT_REMOTE_SAMPLING=1. Spans dropped
	// here never reach the gateway, so its exact request counts see only what is kept.
	RemoteSampling bool
	// CodeOrigin stamps code.filepath, code.lineno and code.function on every span (the code that
	// started it), so traces link to the repo. Also on with WT_CODE_ORIGIN=1.
	CodeOrigin       bool
	SamplingInterval time.Duration
}

// String never shows the ingest key.
func (o Options) String() string {
	return "watchtower.Options{Endpoint: " + o.Endpoint + ", IngestKey: " + mask(o.IngestKey) + ", ServiceName: " + o.ServiceName + "}"
}

// GoString keeps %#v from printing the key.
func (o Options) GoString() string { return o.String() }

// LogValue keeps slog from printing the key.
func (o Options) LogValue() slog.Value {
	return slog.GroupValue(slog.String("endpoint", o.Endpoint), slog.String("ingest_key", mask(o.IngestKey)), slog.String("service", o.ServiceName))
}

func mask(key string) string {
	if key == "" {
		return ""
	}
	return "****"
}

func fromEnv(v *string, name string) {
	if *v == "" {
		*v = os.Getenv(name)
	}
}

func (o Options) resolve() (Options, error) {
	fromEnv(&o.IngestKey, "WT_INGEST_KEY")
	fromEnv(&o.Endpoint, "WT_OTLP_ENDPOINT")
	fromEnv(&o.ServiceName, "WT_SERVICE")
	fromEnv(&o.Environment, "WT_ENVIRONMENT")
	fromEnv(&o.Release, "WT_RELEASE")
	if o.Endpoint == "" {
		o.Endpoint = DefaultEndpoint
	}
	o.Endpoint = strings.TrimRight(o.Endpoint, "/")
	switch {
	case o.IngestKey == "":
		return o, errors.New("watchtower: IngestKey (WT_INGEST_KEY) is required")
	case o.ServiceName == "":
		return o, errors.New("watchtower: ServiceName (WT_SERVICE) is required")
	}
	fromEnv(&o.APIURL, "WT_API_URL")
	if o.APIURL == "" {
		o.APIURL = o.Endpoint
	}
	o.APIURL = strings.TrimRight(o.APIURL, "/")
	o.CaptureRules = o.CaptureRules || os.Getenv("WT_CAPTURE_RULES") == "1"
	o.RemoteSampling = o.RemoteSampling || os.Getenv("WT_REMOTE_SAMPLING") == "1"
	o.CodeOrigin = o.CodeOrigin || os.Getenv("WT_CODE_ORIGIN") == "1"
	if s, err := strconv.Atoi(os.Getenv("WT_SAMPLING_INTERVAL_S")); o.SamplingInterval == 0 && err == nil && s > 0 {
		o.SamplingInterval = time.Duration(s) * time.Second
	}
	if o.Signals == 0 {
		o.Signals = Traces
		if switchOn("WATCHTOWER_METRICS", "OTEL_METRICS_EXPORTER") {
			o.Signals |= Metrics
		}
		if switchOn("WATCHTOWER_LOGS", "OTEL_LOGS_EXPORTER") {
			o.Signals |= Logs
		}
	}
	return o, nil
}

// switchOn reads a WATCHTOWER_* switch (on unless off/false/0/no); otelExporter=none also turns it off.
func switchOn(env, otelExporter string) bool {
	if strings.EqualFold(strings.TrimSpace(os.Getenv(otelExporter)), "none") {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv(env))) {
	case "off", "false", "0", "no":
		return false
	}
	return true
}

// safeAttrs drops attributes in the reserved wt.* namespace (the gateway deletes them anyway).
func safeAttrs(in []attribute.KeyValue) []attribute.KeyValue {
	out := make([]attribute.KeyValue, 0, len(in))
	for _, kv := range in {
		if !strings.HasPrefix(string(kv.Key), "wt.") {
			out = append(out, kv)
		}
	}
	return out
}
