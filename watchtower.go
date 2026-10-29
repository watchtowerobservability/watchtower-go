// Package watchtower configures the OpenTelemetry Go SDK to send traces, metrics and logs to
// Watchtower over OTLP/HTTP, with an optional disk buffer for offline periods, and captures
// errors and panics onto the active span so they link to traces and logs.
package watchtower

import (
	"context"
	"errors"
	"net/http"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/log/global"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/watchtowerobservability/watchtower-go/internal/wal"
)

// Init installs global tracer, meter and logger providers that export to Watchtower and returns
// a shutdown function that flushes and stops them (call it before the process exits).
func Init(ctx context.Context, opt Options) (func(context.Context) error, error) {
	opt, err := opt.resolve()
	if err != nil {
		return nil, err
	}
	setRedact(opt.Redact)
	client := &http.Client{Timeout: 30 * time.Second}
	var w *wal.WAL
	if opt.BufferDir != "" {
		if w, err = wal.Open(opt.BufferDir, opt.BufferMaxBytes); err != nil {
			return nil, err
		}
		client.Transport = newTransport(nil, opt.IngestKey, w)
	}
	attrs := append(safeAttrs(opt.Attributes), attribute.String("service.name", opt.ServiceName))
	if opt.Release != "" {
		attrs = append(attrs, attribute.String("service.version", opt.Release))
	}
	if opt.Environment != "" {
		attrs = append(attrs, attribute.String("deployment.environment.name", opt.Environment))
	}
	res := resource.NewSchemaless(attrs...)
	hdr := map[string]string{"Authorization": "Bearer " + opt.IngestKey}

	var shutdowns []func(context.Context) error
	fail := func(err error) (func(context.Context) error, error) {
		_ = runAll(ctx, shutdowns)
		return nil, err
	}
	if opt.Signals&Traces != 0 {
		exp, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(opt.Endpoint+"/v1/traces"), otlptracehttp.WithHeaders(hdr),
			otlptracehttp.WithCompression(otlptracehttp.GzipCompression), otlptracehttp.WithHTTPClient(client))
		if err != nil {
			return fail(err)
		}
		tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp), sdktrace.WithResource(res))
		otel.SetTracerProvider(tp)
		shutdowns = append(shutdowns, tp.Shutdown)
	}
	if opt.Signals&Metrics != 0 {
		exp, err := otlpmetrichttp.New(ctx, otlpmetrichttp.WithEndpointURL(opt.Endpoint+"/v1/metrics"), otlpmetrichttp.WithHeaders(hdr),
			otlpmetrichttp.WithCompression(otlpmetrichttp.GzipCompression), otlpmetrichttp.WithHTTPClient(client))
		if err != nil {
			return fail(err)
		}
		mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp)), sdkmetric.WithResource(res))
		otel.SetMeterProvider(mp)
		shutdowns = append(shutdowns, mp.Shutdown)
	}
	if opt.Signals&Logs != 0 {
		exp, err := otlploghttp.New(ctx, otlploghttp.WithEndpointURL(opt.Endpoint+"/v1/logs"), otlploghttp.WithHeaders(hdr),
			otlploghttp.WithCompression(otlploghttp.GzipCompression), otlploghttp.WithHTTPClient(client))
		if err != nil {
			return fail(err)
		}
		lp := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewBatchProcessor(exp)), sdklog.WithResource(res))
		global.SetLoggerProvider(lp)
		shutdowns = append(shutdowns, lp.Shutdown)
	}
	if w != nil { // closed last: the providers' final flush may still append
		shutdowns = append(shutdowns, func(context.Context) error { return w.Close() })
	}
	return func(ctx context.Context) error { return runAll(ctx, shutdowns) }, nil
}

func runAll(ctx context.Context, fns []func(context.Context) error) error {
	var errs []error
	for _, f := range fns {
		errs = append(errs, f(ctx))
	}
	return errors.Join(errs...)
}
