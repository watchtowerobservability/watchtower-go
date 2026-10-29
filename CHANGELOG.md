# Changelog

## 0.1.0

- Initial release, extended from the internal `wtotel` package.
- `watchtower.Init` over the OpenTelemetry Go SDK (traces, metrics, logs; OTLP/HTTP, gzip), `Authorization: Bearer` key, `WT_*` environment configuration, disk offline buffer, redaction hook.
- `CaptureError` and `CapturePanic` with stack traces and trace ids; `SlogHandler` for `log/slog`.
- Integrations: `nethttp`, `chi`, `gin`, `echo`, `fiber`, `grpc` (server and client), `sql` (database/sql).
