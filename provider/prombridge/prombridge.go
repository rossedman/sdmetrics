// Package prombridge publishes metrics from a Prometheus registry through
// a systemd report provider, so a program already instrumented with
// client_golang joins systemd-report without re-instrumenting:
//
//	p, err := provider.New("io.example.Api")
//	if err != nil {
//		return err
//	}
//	p.Register(prombridge.New(prometheus.DefaultGatherer))
//	go p.Serve(ctx)
//
// Each read gathers the registry. Counters become counters, gauges and
// untyped metrics gauges, and labels become fields. Histograms become a
// Name_bucket counter with an "le" field plus Name_sum and Name_count
// counters, as in the Prometheus text format; summaries become a Name
// gauge with a "quantile" field plus Name_sum and Name_count. Metric
// names are kept when systemd accepts them (letters, digits and single
// underscores) and sanitized otherwise.
package prombridge

import (
	"context"
	"math"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/rossedman/sdmetrics/internal/names"
	"github.com/rossedman/sdmetrics/provider"
)

// Collector exposes a Prometheus Gatherer as a provider.Collector.
type Collector struct {
	g prometheus.Gatherer
}

// New returns a Collector for g, such as a *prometheus.Registry or
// prometheus.DefaultGatherer.
func New(g prometheus.Gatherer) *Collector { return &Collector{g: g} }

// Collect gathers the registry and converts it. Gathering errors that
// still return metrics, such as one failing collector, are returned
// alongside the metrics that were gathered.
func (c *Collector) Collect(context.Context) ([]provider.Family, error) {
	mfs, err := c.g.Gather()
	out := make([]provider.Family, 0, len(mfs))
	for _, mf := range mfs {
		out = append(out, convert(mf)...)
	}
	return out, err
}

// labels returns the metric's labels as a field map with the extra key-value
// pairs added, or nil if there are none.
func labels(m *dto.Metric, extra ...string) map[string]string {
	if len(m.GetLabel()) == 0 && len(extra) == 0 {
		return nil
	}
	f := make(map[string]string, len(m.GetLabel())+len(extra)/2)
	for _, lp := range m.GetLabel() {
		f[lp.GetName()] = lp.GetValue()
	}
	for i := 0; i+1 < len(extra); i += 2 {
		f[extra[i]] = extra[i+1]
	}
	return f
}

// counterValue reports a counter as an unsigned integer when it is one,
// as systemd's own counters are, and as a float otherwise.
func counterValue(v float64) any {
	if v >= 0 && v <= 1<<53 && v == math.Trunc(v) {
		return uint64(v)
	}
	return v
}

// formatBound formats a bucket bound or quantile, writing +Inf for infinity.
func formatBound(b float64) string {
	if math.IsInf(b, 1) {
		return "+Inf"
	}
	return strconv.FormatFloat(b, 'g', -1, 64)
}

// convert converts one Prometheus metric family to provider families.
func convert(mf *dto.MetricFamily) []provider.Family {
	name := names.SanitizeField(mf.GetName())
	help := mf.GetHelp()
	switch mf.GetType() {
	case dto.MetricType_COUNTER:
		f := provider.Family{Name: name, Description: help, Type: provider.TypeCounter}
		for _, m := range mf.GetMetric() {
			f.Samples = append(f.Samples, provider.Sample{Fields: labels(m), Value: counterValue(m.GetCounter().GetValue())})
		}
		return []provider.Family{f}

	case dto.MetricType_GAUGE, dto.MetricType_UNTYPED:
		f := provider.Family{Name: name, Description: help, Type: provider.TypeGauge}
		for _, m := range mf.GetMetric() {
			v := m.GetGauge().GetValue()
			if mf.GetType() == dto.MetricType_UNTYPED {
				v = m.GetUntyped().GetValue()
			}
			f.Samples = append(f.Samples, provider.Sample{Fields: labels(m), Value: v})
		}
		return []provider.Family{f}

	case dto.MetricType_SUMMARY:
		q := provider.Family{Name: name, Description: help, Type: provider.TypeGauge}
		sum := provider.Family{Name: name + "_sum", Description: help + " (sum)", Type: provider.TypeCounter}
		count := provider.Family{Name: name + "_count", Description: help + " (count)", Type: provider.TypeCounter}
		for _, m := range mf.GetMetric() {
			s := m.GetSummary()
			for _, qt := range s.GetQuantile() {
				q.Samples = append(q.Samples, provider.Sample{Fields: labels(m, "quantile", formatBound(qt.GetQuantile())), Value: qt.GetValue()})
			}
			sum.Samples = append(sum.Samples, provider.Sample{Fields: labels(m), Value: s.GetSampleSum()})
			count.Samples = append(count.Samples, provider.Sample{Fields: labels(m), Value: s.GetSampleCount()})
		}
		return []provider.Family{q, sum, count}

	case dto.MetricType_HISTOGRAM, dto.MetricType_GAUGE_HISTOGRAM:
		t := provider.TypeCounter
		if mf.GetType() == dto.MetricType_GAUGE_HISTOGRAM {
			t = provider.TypeGauge
		}
		buckets := provider.Family{Name: name + "_bucket", Description: help + " (cumulative buckets)", Type: t}
		sum := provider.Family{Name: name + "_sum", Description: help + " (sum)", Type: t}
		count := provider.Family{Name: name + "_count", Description: help + " (count)", Type: t}
		for _, m := range mf.GetMetric() {
			h := m.GetHistogram()
			infSeen := false
			for _, b := range h.GetBucket() {
				infSeen = infSeen || math.IsInf(b.GetUpperBound(), 1)
				buckets.Samples = append(buckets.Samples, provider.Sample{Fields: labels(m, "le", formatBound(b.GetUpperBound())), Value: b.GetCumulativeCount()})
			}
			if !infSeen && len(h.GetBucket()) > 0 {
				buckets.Samples = append(buckets.Samples, provider.Sample{Fields: labels(m, "le", "+Inf"), Value: h.GetSampleCount()})
			}
			sum.Samples = append(sum.Samples, provider.Sample{Fields: labels(m), Value: h.GetSampleSum()})
			count.Samples = append(count.Samples, provider.Sample{Fields: labels(m), Value: h.GetSampleCount()})
		}
		out := []provider.Family{sum, count}
		if len(buckets.Samples) > 0 {
			out = append([]provider.Family{buckets}, out...)
		}
		return out
	}
	return nil
}
