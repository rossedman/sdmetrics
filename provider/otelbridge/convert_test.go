package otelbridge

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/rossedman/sdmetrics/provider"
)

func flat(fams []provider.Family) string {
	var out []string
	for _, f := range fams {
		for _, s := range f.Samples {
			var fs []string
			for k, v := range s.Fields {
				fs = append(fs, k+"="+v)
			}
			sort.Strings(fs)
			out = append(out, fmt.Sprintf("%s{%s} %s %v", f.Name, strings.Join(fs, ","), f.Type, s.Value))
		}
	}
	sort.Strings(out)
	return strings.Join(out, "\n")
}

func TestConvertAllKinds(t *testing.T) {
	attrs := attribute.NewSet(attribute.String("k", "v"))
	for _, tc := range []struct {
		m    metricdata.Metrics
		want string
	}{
		{metricdata.Metrics{Name: "g", Data: metricdata.Gauge[int64]{DataPoints: []metricdata.DataPoint[int64]{{Value: 3}}}}, "g{} gauge 3"},
		{metricdata.Metrics{Name: "gf", Data: metricdata.Gauge[float64]{DataPoints: []metricdata.DataPoint[float64]{{Attributes: attrs, Value: 1.5}}}}, "gf{k=v} gauge 1.5"},
		{metricdata.Metrics{Name: "s", Data: metricdata.Sum[float64]{IsMonotonic: true, Temporality: metricdata.CumulativeTemporality, DataPoints: []metricdata.DataPoint[float64]{{Value: 2}}}}, "s{} counter 2"},
		{metricdata.Metrics{Name: "d", Data: metricdata.Sum[int64]{IsMonotonic: true, Temporality: metricdata.DeltaTemporality, DataPoints: []metricdata.DataPoint[int64]{{Value: 4}}}}, "d{} gauge 4"},
		{metricdata.Metrics{Name: "h", Unit: "ms", Data: metricdata.Histogram[int64]{Temporality: metricdata.DeltaTemporality, DataPoints: []metricdata.HistogramDataPoint[int64]{{Bounds: []float64{10}, BucketCounts: []uint64{1, 2}, Count: 3, Sum: 25}}}},
			"h_milliseconds_bucket{le=+Inf} gauge 3\nh_milliseconds_bucket{le=10} gauge 1\nh_milliseconds_count{} gauge 3\nh_milliseconds_sum{} gauge 25"},
		{metricdata.Metrics{Name: "e", Data: metricdata.ExponentialHistogram[float64]{DataPoints: []metricdata.ExponentialHistogramDataPoint[float64]{{Count: 2, Sum: 1.5}}}}, "e_count{} counter 2\ne_sum{} counter 1.5"},
		{metricdata.Metrics{Name: "ei", Data: metricdata.ExponentialHistogram[int64]{DataPoints: []metricdata.ExponentialHistogramDataPoint[int64]{{Count: 1, Sum: 7}}}}, "ei_count{} counter 1\nei_sum{} counter 7"},
		{metricdata.Metrics{Name: "q", Data: metricdata.Summary{DataPoints: []metricdata.SummaryDataPoint{{Count: 4, Sum: 8, QuantileValues: []metricdata.QuantileValue{{Quantile: 0.5, Value: 2}, {Quantile: 0.9, Value: math.NaN()}}}}}},
			"q_count{} counter 4\nq_sum{} counter 8\nq{quantile=0.5} gauge 2"},
		{metricdata.Metrics{Name: "x", Data: nil}, ""},
	} {
		if got := flat(convert(tc.m)); got != tc.want {
			t.Errorf("%s:\n got %s\nwant %s", tc.m.Name, got, tc.want)
		}
	}
}

func TestCollectError(t *testing.T) {
	r := sdkmetric.NewManualReader()
	// A reader not registered with a MeterProvider cannot collect.
	if _, err := New(r).Collect(context.Background()); err == nil {
		t.Fatal("unregistered reader collected")
	}
	r.Shutdown(context.Background())
	_, err := New(r).Collect(context.Background())
	if err == nil || errors.Is(err, nil) {
		t.Fatal("shut down reader collected")
	}
}
