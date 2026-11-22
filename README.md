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
| database/sql | `.../watchtower-go/sql` | `Open(driver, dsn, system)`: query spans with `db.query.hash` (decimal XXH3, see docs/product/instrumentation/sql-normalization.md) and `db.query.summary` (no raw text; `WithQueryText(system)` adds sanitized text), `ObservePool(db, name)`, `ObserveFiles` for SQLite/DuckDB file and WAL size |
| bbolt | `.../watchtower-go/datastores/bbolt` | `View/Update/Batch(ctx, db, fn)` spans, `Observe(db)`: file size, free pages, open transactions |
| badger (v4) | `.../watchtower-go/datastores/badger` | `View/Update/RunValueLogGC` spans, `Observe(db)`: LSM and value-log size, compaction debt, block cache |
| pebble (v2) | `.../watchtower-go/datastores/pebble` | `DB{db, dir}`: Get/Set/Delete/Apply spans, `Observe()`: disk, WAL, memtable, cache, compactions, flushes |
| goleveldb | `.../watchtower-go/datastores/leveldb` | `DB{db, dir}`: Get/Put/Delete/Write spans, `Observe()`: level sizes, write stalls, compaction time, IO |
| buntdb | `.../watchtower-go/datastores/buntdb` | `DB{db, path}`: View/Update spans, `Observe()`: key count, file size |
| ristretto (v2) | `.../watchtower-go/datastores/ristretto` | `Observe(cache, name)`: hits, misses, evictions, rejections, cost in use |
| any other embedded store | `.../watchtower-go/embedded` | `Span(ctx, Op, fn)` and `Observe(system, ns, fn)` with the shared `wt.embedded.*` names |

The `datastores/*` packages live in a separate module (`go get github.com/watchtowerobservability/watchtower-go/datastores`), so the core SDK does not pull in pebble, badger and the rest.
| log/slog | root package | `SlogHandler(next)`: OTLP logs with trace ids |

## App and API Protection

An in-app WAF fed by your project's ruleset (key scope `probes:read`). It is built into
`wtnethttp.Handler`; for other routers, wrap them with `watchtower.AppsecMiddleware`. In `monitor`
mode it records attacks on the span. In `block` mode, and always for denylisted IPs and users, it
answers 403 before the handler runs.

```go
watchtower.StartAppsec(ctx, nil, os.Getenv("WT_API_URL"), os.Getenv("WT_INGEST_KEY"), "checkout")
// in the login handler: watchtower.TrackLogin(r.Context(), email, ok)
```

See `docs/product/security/app-api-protection.md`.

## Configuration

| Option | Environment | Default |
| --- | --- | --- |
| `IngestKey` | `WT_INGEST_KEY` | required, sent as `Authorization: Bearer`, masked in any output |
| `Endpoint` | `WT_OTLP_ENDPOINT` | `http://localhost:4318` |
| `ServiceName` | `WT_SERVICE` | required |
| `Environment` | `WT_ENVIRONMENT` | none |
| `Release` | `WT_RELEASE` | none |
| `Signals` | `WATCHTOWER_LOGS`, `WATCHTOWER_METRICS` (`on`/`off`; `OTEL_LOGS_EXPORTER` / `OTEL_METRICS_EXPORTER=none` also turn one off) | traces, metrics and logs |
| `BufferDir` | none | offline buffer off (covers all three signals) |
| `Redact` | none | none; called for captured messages and string attributes |

Attributes under the reserved `wt.` namespace are dropped before sending.

Metrics: `otel.Meter("app")` records through the global meter provider `Init` installs; the periodic
reader follows `OTEL_METRIC_EXPORT_INTERVAL`. Counters and histograms use delta temporality unless
`OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE=cumulative`. For Go runtime metrics (GC, memory,
goroutines) add `go.opentelemetry.io/contrib/instrumentation/runtime` and call `runtime.Start()` after `Init`.

## Provenance

Every release carries a GitHub build attestation on its source archive. Verify one with
`gh attestation verify <file> --repo watchtowerobservability/watchtower-go`.

## License

Apache-2.0. Source of truth is the Watchtower monorepo; this repository is a published mirror.
