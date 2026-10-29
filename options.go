package watchtower

import (
	"errors"
	"log/slog"
	"os"
	"strings"

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
	Signals    Signal // zero means Traces|Logs
	// BufferDir enables the offline buffer: failed exports are written there and replayed
	// oldest first after the gateway answers again (at-least-once).
	BufferDir      string
	BufferMaxBytes int64 // zero means the WAL default (512 MiB)
	// Redact, when set, is called for every captured error message and string attribute and
	// returns the value to send (return "" to drop the content).
	Redact func(key, value string) string
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
	if o.Signals == 0 {
		o.Signals = Traces | Logs
	}
	return o, nil
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
