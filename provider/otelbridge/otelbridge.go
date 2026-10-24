// Package otelbridge publishes metrics from the OpenTelemetry Go SDK
// through a systemd report provider, so a program already instrumented
// with OpenTelemetry joins systemd-report without re-instrumenting:
//
//	reader := sdkmetric.NewManualReader()
//	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
//
//	p, err := provider.New("io.example.Api")
//	if err != nil {
//		return err
//	}
//	p.Register(otelbridge.New(reader))
//	go p.Serve(ctx)
//
// Each read collects from the reader, which must use cumulative
// temporality (the default). Monotonic sums become counters; gauges and
// non-monotonic sums become gauges; attributes become fields. Explicit
// bucket histograms become a Name_bucket counter with an "le" field plus
// Name_sum and Name_count counters; exponential histograms keep only
// Name_sum and Name_count. Dots and other characters systemd does not
// accept in a metric name become underscores, and the units s, ms and By
// are appended as _seconds, _milliseconds and _bytes:
// http.server.request.duration (unit s) is published as
// http_server_request_duration_seconds.
package otelbridge

import (
	"context"
	"math"
	"strconv"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/rossedman/sdmetrics/internal/names"
	"github.com/rossedman/sdmetrics/provider"
)

// Collector exposes an OpenTelemetry SDK reader as a provider.Collector.
type Collector struct {
	r sdkmetric.Reader
}

// New returns a Collector reading from r, typically a ManualReader also
// passed to the MeterProvider with sdkmetric.WithReader.
func New(r sdkmetric.Reader) *Collector { return &Collector{r: r} }

// Collect collects from the reader and converts the result.
func (c *Collector) Collect(ctx context.Context) ([]provider.Family, error) {
	var rm metricdata.ResourceMetrics
	if err := c.r.Collect(ctx, &rm); err != nil {
		return nil, err
	}
	var out []provider.Family
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			out = append(out, convert(m)...)
		}
	}
	return out, nil
}

var unitSuffix = map[string]string{"s": "seconds", "ms": "milliseconds", "By": "bytes"}

// Name returns the systemd metric name for an OpenTelemetry instrument.
func Name(name, unit string) string {
	n := names.SanitizeField(name)
	if s := unitSuffix[unit]; s != "" && !strings.HasSuffix(n, "_"+s) {
		n += "_" + s
	}
	return n
}

// fields returns the attributes as a field map with the extra key-value pairs
// added, or nil if there are none.
func fields(set attribute.Set, extra ...string) map[string]string {
	if set.Len() == 0 && len(extra) == 0 {
		return nil
	}
	f := make(map[string]string, set.Len()+len(extra)/2)
	for it := set.Iter(); it.Next(); {
		kv := it.Attribute()
		f[string(kv.Key)] = kv.Value.String()
	}
	for i := 0; i+1 < len(extra); i += 2 {
		f[extra[i]] = extra[i+1]
	}
	return f
}

// points converts data points to samples.
func points[N int64 | float64](dps []metricdata.DataPoint[N]) []provider.Sample {
	out := make([]provider.Sample, len(dps))
	for i, dp := range dps {
		out[i] = provider.Sample{Fields: fields(dp.Attributes), Value: dp.Value}
	}
	return out
}

// histogram converts a histogram to cumulative bucket, sum and count families.
func histogram[N int64 | float64](name, help string, h metricdata.Histogram[N]) []provider.Family {
	buckets := provider.Family{Name: name + "_bucket", Description: help + " (cumulative buckets)", Type: provider.TypeCounter}
	sum := provider.Family{Name: name + "_sum", Description: help + " (sum)", Type: provider.TypeCounter}
	count := provider.Family{Name: name + "_count", Description: help + " (count)", Type: provider.TypeCounter}
	for _, dp := range h.DataPoints {
		var cum uint64
		for i, n := range dp.BucketCounts {
			cum += n
			le := "+Inf"
			if i < len(dp.Bounds) {
				le = strconv.FormatFloat(dp.Bounds[i], 'g', -1, 64)
			}
			buckets.Samples = append(buckets.Samples, provider.Sample{Fields: fields(dp.Attributes, "le", le), Value: cum})
		}
		sum.Samples = append(sum.Samples, provider.Sample{Fields: fields(dp.Attributes), Value: dp.Sum})
		count.Samples = append(count.Samples, provider.Sample{Fields: fields(dp.Attributes), Value: dp.Count})
	}
	if h.Temporality == metricdata.DeltaTemporality {
		buckets.Type, sum.Type, count.Type = provider.TypeGauge, provider.TypeGauge, provider.TypeGauge
	}
	return []provider.Family{buckets, sum, count}
}

// expHistogram converts an exponential histogram to sum and count families;
// its buckets have no Prometheus-style equivalent and are dropped.
func expHistogram[N int64 | float64](name, help string, h metricdata.ExponentialHistogram[N]) []provider.Family {
	sum := provider.Family{Name: name + "_sum", Description: help + " (sum)", Type: provider.TypeCounter}
	count := provider.Family{Name: name + "_count", Description: help + " (count)", Type: provider.TypeCounter}
	for _, dp := range h.DataPoints {
		sum.Samples = append(sum.Samples, provider.Sample{Fields: fields(dp.Attributes), Value: dp.Sum})
		count.Samples = append(count.Samples, provider.Sample{Fields: fields(dp.Attributes), Value: dp.Count})
	}
	return []provider.Family{sum, count}
}

// sumFamily converts a sum, which is a counter only if cumulative and monotonic.
func sumFamily[N int64 | float64](name, help string, s metricdata.Sum[N]) provider.Family {
	t := provider.TypeGauge
	// Only a cumulative monotonic sum is a counter; a delta sum resets on
	// every collection.
	if s.IsMonotonic && s.Temporality == metricdata.CumulativeTemporality {
		t = provider.TypeCounter
	}
	return provider.Family{Name: name, Description: help, Type: t, Samples: points(s.DataPoints)}
}

// convert converts one OpenTelemetry metric to provider families.
func convert(m metricdata.Metrics) []provider.Family {
	name, help := Name(m.Name, m.Unit), m.Description
	switch d := m.Data.(type) {
	case metricdata.Gauge[int64]:
		return []provider.Family{{Name: name, Description: help, Type: provider.TypeGauge, Samples: points(d.DataPoints)}}
	case metricdata.Gauge[float64]:
		return []provider.Family{{Name: name, Description: help, Type: provider.TypeGauge, Samples: points(d.DataPoints)}}
	case metricdata.Sum[int64]:
		return []provider.Family{sumFamily(name, help, d)}
	case metricdata.Sum[float64]:
		return []provider.Family{sumFamily(name, help, d)}
	case metricdata.Histogram[int64]:
		return histogram(name, help, d)
	case metricdata.Histogram[float64]:
		return histogram(name, help, d)
	case metricdata.ExponentialHistogram[int64]:
		return expHistogram(name, help, d)
	case metricdata.ExponentialHistogram[float64]:
		return expHistogram(name, help, d)
	case metricdata.Summary:
		q := provider.Family{Name: name, Description: help, Type: provider.TypeGauge}
		sum := provider.Family{Name: name + "_sum", Description: help + " (sum)", Type: provider.TypeCounter}
		count := provider.Family{Name: name + "_count", Description: help + " (count)", Type: provider.TypeCounter}
		for _, dp := range d.DataPoints {
			for _, qv := range dp.QuantileValues {
				if math.IsNaN(qv.Value) {
					continue
				}
				q.Samples = append(q.Samples, provider.Sample{Fields: fields(dp.Attributes, "quantile", strconv.FormatFloat(qv.Quantile, 'g', -1, 64)), Value: qv.Value})
			}
			sum.Samples = append(sum.Samples, provider.Sample{Fields: fields(dp.Attributes), Value: dp.Sum})
			count.Samples = append(count.Samples, provider.Sample{Fields: fields(dp.Attributes), Value: dp.Count})
		}
		return []provider.Family{q, sum, count}
	}
	return nil
}
