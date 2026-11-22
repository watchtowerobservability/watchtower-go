// Package wtsql traces database/sql calls (query, exec, begin, commit, prepare) with otelsql.
// Works for any driver, including embedded SQLite (mattn/go-sqlite3, modernc.org/sqlite) and
// DuckDB (marcboeker/go-duckdb): pass system "sqlite" or "duckdb" and call ObserveFiles.
package wtsql

import (
	"context"
	"database/sql"
	"database/sql/driver"

	"github.com/XSAM/otelsql"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	wtembedded "github.com/watchtowerobservability/watchtower-go/embedded"
	"github.com/watchtowerobservability/watchtower-go/internal/sqlnorm"
)

// WithQueryText (same system as Open) opts in to db.query.text on spans, sanitized (every literal replaced by ?). Off by
// default: spans carry only db.query.hash and db.query.summary.
func WithQueryText(system string) otelsql.Option {
	return otelsql.WithAttributesGetter(queryAttrs(system, true))
}

// Open is sql.Open with tracing. system is the OTel db.system.name value ("postgresql", "mysql",
// "sqlite", "duckdb", ...). Spans never hold the raw statement or argument values.
func Open(driverName, dataSourceName, system string, opts ...otelsql.Option) (*sql.DB, error) {
	opts = append([]otelsql.Option{
		otelsql.WithAttributes(attribute.String("db.system", system), attribute.String("db.system.name", system)),
		otelsql.WithSpanOptions(otelsql.SpanOptions{DisableQuery: true}),
		otelsql.WithAttributesGetter(queryAttrs(system, false)),
	}, opts...)
	return otelsql.Open(driverName, dataSourceName, opts...)
}

func queryAttrs(system string, withText bool) otelsql.AttributesGetter {
	return func(_ context.Context, _ otelsql.Method, query string, _ []driver.NamedValue) []attribute.KeyValue {
		return QueryAttributes(system, query, withText)
	}
}

// QueryAttributes returns db.query.hash, db.query.summary, db.operation.name, db.collection.name
// and, when withText, the sanitized db.query.text. The hash (decimal XXH3-64, see
// docs/product/instrumentation/sql-normalization.md) matches the worker and every other SDK.
func QueryAttributes(system, query string, withText bool) []attribute.KeyValue {
	if query == "" {
		return nil
	}
	n := sqlnorm.Normalize(query, sqlnorm.PG(system))
	attrs := []attribute.KeyValue{attribute.String("db.query.hash", sqlnorm.Hash(n))}
	op, table := sqlnorm.Summary(n)
	if op != "" {
		summary := op
		if table != "" {
			summary += " " + table
			attrs = append(attrs, attribute.String("db.collection.name", table))
		}
		attrs = append(attrs, attribute.String("db.operation.name", op), attribute.String("db.query.summary", summary))
	}
	if withText {
		attrs = append(attrs, attribute.String("db.query.text", n))
	}
	return attrs
}

// ObservePool reports db.client.connection.* for db: count{state=idle|used}, max, and the
// cumulative wait_time (ms) and wait count. pool names the pool (db.client.connection.pool.name).
func ObservePool(db *sql.DB, pool string) (metric.Registration, error) {
	m := otel.Meter("github.com/watchtowerobservability/watchtower-go/sql")
	count, err := m.Int64ObservableUpDownCounter("db.client.connection.count", metric.WithUnit("{connection}"))
	if err != nil {
		return nil, err
	}
	maxC, err := m.Int64ObservableUpDownCounter("db.client.connection.max", metric.WithUnit("{connection}"))
	if err != nil {
		return nil, err
	}
	waits, err := m.Int64ObservableCounter("db.client.connection.waits", metric.WithUnit("{wait}"))
	if err != nil {
		return nil, err
	}
	waitMs, err := m.Int64ObservableCounter("db.client.connection.wait_time", metric.WithUnit("ms"))
	if err != nil {
		return nil, err
	}
	name := attribute.String("db.client.connection.pool.name", pool)
	return m.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		s := db.Stats()
		o.ObserveInt64(count, int64(s.Idle), metric.WithAttributes(name, attribute.String("db.client.connection.state", "idle")))
		o.ObserveInt64(count, int64(s.InUse), metric.WithAttributes(name, attribute.String("db.client.connection.state", "used")))
		o.ObserveInt64(maxC, int64(s.MaxOpenConnections), metric.WithAttributes(name))
		o.ObserveInt64(waits, s.WaitCount, metric.WithAttributes(name))
		o.ObserveInt64(waitMs, s.WaitDuration.Milliseconds(), metric.WithAttributes(name))
		return nil
	}, count, maxC, waits, waitMs)
}

// ObserveFiles reports wt.embedded.file.size and wal.size for a file-backed engine such as SQLite
// (wal: path+"-wal") or DuckDB (wal: path+".wal").
func ObserveFiles(system, path, walPath string) (metric.Registration, error) {
	return wtembedded.ObserveFiles(system, path, walPath)
}
