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

func TestSpansCarryHashNotRawText(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr)))
	_, mock, err := sqlmock.NewWithDSN("wt_test_hash")
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectExec("DELETE FROM jobs").WillReturnResult(sqlmock.NewResult(0, 1))
	db, err := Open("sqlmock", "wt_test_hash", "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(context.Background(), "DELETE FROM jobs WHERE owner = 'alice@example.com'"); err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	for _, s := range sr.Ended() {
		for _, kv := range s.Attributes() {
			seen[string(kv.Key)] = kv.Value.Emit()
		}
	}
	if seen["db.query.summary"] != "DELETE jobs" || seen["db.query.hash"] == "" {
		t.Fatalf("attributes %v", seen)
	}
	if _, ok := seen["db.query.text"]; ok {
		t.Fatal("raw query text must be off by default")
	}
}

func TestQueryAttributesOptInTextIsSanitized(t *testing.T) {
	for _, kv := range QueryAttributes("sqlite", "select 1 from t where e = 'alice@example.com'", true) {
		if kv.Key == "db.query.text" && kv.Value.AsString() != "select ? from t where e = ?" {
			t.Fatalf("text %q", kv.Value.AsString())
		}
	}
}
