// Package wtsql traces database/sql calls (query, exec, begin, commit, prepare) with otelsql.
package wtsql

import (
	"database/sql"

	"github.com/XSAM/otelsql"
	"go.opentelemetry.io/otel/attribute"
)

// Open is sql.Open with tracing. system is the OTel db.system value ("postgresql", "mysql",
// "sqlite", ...). Span statements hold the query text with placeholders, never argument values.
func Open(driverName, dataSourceName, system string, opts ...otelsql.Option) (*sql.DB, error) {
	opts = append([]otelsql.Option{otelsql.WithAttributes(attribute.String("db.system", system))}, opts...)
	return otelsql.Open(driverName, dataSourceName, opts...)
}
