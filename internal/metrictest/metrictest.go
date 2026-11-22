// Package metrictest reads a ManualReader into name -> last int64 value for adapter tests.
package metrictest

import (
	"context"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// Collect returns metric name -> value of its last int64 data point.
func Collect(t testing.TB, rd *sdkmetric.ManualReader) map[string]int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := rd.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	got := map[string]int64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			switch d := m.Data.(type) {
			case metricdata.Gauge[int64]:
				for _, p := range d.DataPoints {
					got[m.Name] = p.Value
				}
			case metricdata.Sum[int64]:
				for _, p := range d.DataPoints {
					got[m.Name] = p.Value
				}
			}
		}
	}
	return got
}
