package wtsql

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestQueriesProduceSpansWithDBSystem(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr)))
	_, mock, err := sqlmock.NewWithDSN("wt_test")
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery("SELECT 1").WillReturnRows(sqlmock.NewRows([]string{"x"}).AddRow(1))

	db, err := Open("sqlmock", "wt_test", "postgresql")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.QueryContext(context.Background(), "SELECT 1")
	if err != nil {
		t.Fatal(err)
	}
	_ = rows.Close()

	for _, s := range sr.Ended() {
		for _, kv := range s.Attributes() {
			if kv.Key == "db.system" && kv.Value.AsString() == "postgresql" {
				return
			}
		}
	}
	t.Fatalf("no span carries db.system among %d spans", len(sr.Ended()))
}
