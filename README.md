# watchtower-go

[![CI](https://github.com/watchtowerobservability/watchtower-go/actions/workflows/ci.yml/badge.svg)](https://github.com/watchtowerobservability/watchtower-go/actions/workflows/ci.yml)

Watchtower for Go: a thin layer over the OpenTelemetry Go SDK. It sets the key header, the endpoint
default, a disk offline buffer, `wt.*`-safe resource attributes and a redaction hook, and adds error and
panic capture linked to traces and logs, a `log/slog` bridge and framework integrations.

## Install

```sh
go get github.com/watchtowerobservability/watchtower-go
```

## Quick start

```go
shutdown, err := watchtower.Init(ctx, watchtower.Options{BufferDir: ".wt-buffer"}) // reads WT_* from the environment
if err != nil { log.Fatal(err) }
defer shutdown(ctx)
slog.SetDefault(slog.New(watchtower.SlogHandler(slog.NewTextHandler(os.Stderr, nil))))
http.ListenAndServe(":8080", wtnethttp.Handler(mux, "my-service")) // spans + panic capture
```

Capture an error anywhere: `watchtower.CaptureError(ctx, err)` records an OTel `exception` event with a stack
trace on the active span (or a new `error` span) and returns the trace id.

## Integrations

| Package | Import | What you get |
| --- | --- | --- |
| net/http | `.../watchtower-go/nethttp` | `Handler` (server span + panic capture), `Transport` (client spans) |
| chi | `.../watchtower-go/chi` | `Middleware` with route-pattern span names |
| gin | `.../watchtower-go/gin` | `Use(r, service)`: spans, panics, `c.Errors` |
| echo (v4) | `.../watchtower-go/echo` | `Use(e, service)`: spans, panics, 5xx handler errors |
| fiber (v2) | `.../watchtower-go/fiber` | `Middleware()`: spans, W3C extraction, panics |
| gRPC | `.../watchtower-go/grpc` | `ServerOptions()`, `DialOptions()`: spans, panic and error capture |
| database/sql | `.../watchtower-go/sql` | `Open(driver, dsn, system)`: query spans |
| log/slog | root package | `SlogHandler(next)`: OTLP logs with trace ids |

## Configuration

| Option | Environment | Default |
| --- | --- | --- |
| `IngestKey` | `WT_INGEST_KEY` | required, sent as `Authorization: Bearer`, masked in any output |
| `Endpoint` | `WT_OTLP_ENDPOINT` | `http://localhost:4318` |
| `ServiceName` | `WT_SERVICE` | required |
| `Environment` | `WT_ENVIRONMENT` | none |
| `Release` | `WT_RELEASE` | none |
| `BufferDir` | none | offline buffer off |
| `Redact` | none | none; called for captured messages and string attributes |

Attributes under the reserved `wt.` namespace are dropped before sending.

## Provenance

Every release carries a GitHub build attestation on its source archive. Verify one with
`gh attestation verify <file> --repo watchtowerobservability/watchtower-go`.

## License

Apache-2.0. Source of truth is the Watchtower monorepo; this repository is a published mirror.
