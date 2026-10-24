package otelmap

import (
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
)

// ToSDK converts a batch for the OpenTelemetry Go SDK's exporters, such
// as otlpmetricgrpc: one ResourceMetrics per resource, each with a single
// scope.
func ToSDK(b Batch, scopeVersion string) []*metricdata.ResourceMetrics {
	out := make([]*metricdata.ResourceMetrics, 0, len(b.Resources))
	scope := instrumentation.Scope{Name: ScopeName, Version: scopeVersion}
	for _, r := range b.Resources {
		ms := make([]metricdata.Metrics, 0, len(r.Metrics))
		for _, m := range r.Metrics {
			ms = append(ms, sdkMetric(m))
		}
		out = append(out, &metricdata.ResourceMetrics{
			Resource:     resource.NewSchemaless(sdkAttrs(r.Attributes)...),
			ScopeMetrics: []metricdata.ScopeMetrics{{Scope: scope, Metrics: ms}},
		})
	}
	return out
}

// sdkAttrs converts attributes to OpenTelemetry SDK key-values.
func sdkAttrs(attrs []Attribute) []attribute.KeyValue {
	kvs := make([]attribute.KeyValue, len(attrs))
	for i, a := range attrs {
		if a.IsInt {
			kvs[i] = attribute.Int64(a.Key, a.Int)
		} else {
			kvs[i] = attribute.String(a.Key, a.Str)
		}
	}
	return kvs
}

// allInt reports whether every point is an integer, so the metric can use
// int64 data points; mixed metrics use float64.
func allInt(ps []Point) bool {
	for _, p := range ps {
		if !p.IsInt {
			return false
		}
	}
	return true
}

// sdkMetric converts m to SDK metric data, using int64 points when every
// point is an integer and float64 otherwise.
func sdkMetric(m Metric) metricdata.Metrics {
	out := metricdata.Metrics{Name: m.Name, Description: m.Description, Unit: m.Unit}
	if allInt(m.Points) {
		dps := make([]metricdata.DataPoint[int64], len(m.Points))
		for i, p := range m.Points {
			dps[i] = metricdata.DataPoint[int64]{Attributes: attribute.NewSet(sdkAttrs(p.Attributes)...), StartTime: p.Start, Time: p.Time, Value: p.Int}
		}
		if m.Kind == Sum {
			out.Data = metricdata.Sum[int64]{DataPoints: dps, Temporality: metricdata.CumulativeTemporality, IsMonotonic: m.Monotonic}
		} else {
			out.Data = metricdata.Gauge[int64]{DataPoints: dps}
		}
		return out
	}
	dps := make([]metricdata.DataPoint[float64], len(m.Points))
	for i, p := range m.Points {
		dps[i] = metricdata.DataPoint[float64]{Attributes: attribute.NewSet(sdkAttrs(p.Attributes)...), StartTime: p.Start, Time: p.Time, Value: p.Value()}
	}
	if m.Kind == Sum {
		out.Data = metricdata.Sum[float64]{DataPoints: dps, Temporality: metricdata.CumulativeTemporality, IsMonotonic: m.Monotonic}
	} else {
		out.Data = metricdata.Gauge[float64]{DataPoints: dps}
	}
	return out
}
