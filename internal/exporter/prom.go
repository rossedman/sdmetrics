package exporter

import (
	"sort"
	"strings"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/rossedman/sdmetrics/otelmap"
)

// promResourceLabels are the resource attributes copied onto every
// Prometheus sample. Host identity is left to the scrape target labels.
var promResourceLabels = map[string]bool{
	otelmap.AttrSource:      true,
	otelmap.AttrUnit:        true,
	otelmap.AttrServiceName: true,
}

// promCollector serves the latest batch to Prometheus.
type promCollector struct {
	naming otelmap.Naming
	extra  map[string]bool // configured resource attributes, also copied
	batch  atomic.Pointer[otelmap.Batch]
}

func newPromCollector(naming otelmap.Naming, resource map[string]string) *promCollector {
	extra := map[string]bool{}
	for k := range resource {
		extra[k] = true
	}
	return &promCollector{naming: naming, extra: extra}
}

func (c *promCollector) set(b otelmap.Batch) { c.batch.Store(&b) }

// Describe sends nothing: the metrics change with what sources report, so
// the collector is unchecked.
func (c *promCollector) Describe(chan<- *prometheus.Desc) {}

type promSample struct {
	labels map[string]string
	value  float64
}

type promFamily struct {
	name, help string
	typ        prometheus.ValueType
	labels     map[string]bool
	samples    []promSample
}

// Collect converts the latest batch. Families are merged across resources
// and every sample of a family gets the union of its label names, as
// Prometheus requires.
func (c *promCollector) Collect(ch chan<- prometheus.Metric) {
	b := c.batch.Load()
	if b == nil {
		return
	}
	fams := map[string]*promFamily{}
	var order []string
	for _, r := range b.Resources {
		res := map[string]string{}
		for _, a := range r.Attributes {
			if promResourceLabels[a.Key] || c.extra[a.Key] {
				res[promLabel(a.Key)] = a.String()
			}
		}
		for _, m := range r.Metrics {
			name := c.promName(m)
			f, ok := fams[name]
			if !ok {
				typ := prometheus.GaugeValue
				if m.Kind == otelmap.Sum && m.Monotonic {
					typ = prometheus.CounterValue
				}
				help := m.Description
				if help == "" {
					help = m.Name
				}
				f = &promFamily{name: name, help: help, typ: typ, labels: map[string]bool{}}
				fams[name] = f
				order = append(order, name)
			}
			for _, p := range m.Points {
				labels := map[string]string{}
				for _, a := range p.Attributes {
					l := promLabel(a.Key)
					if _, dup := labels[l]; !dup {
						labels[l] = a.String()
					}
				}
				for k, v := range res {
					if _, dup := labels[k]; dup {
						k = "resource_" + k
					}
					labels[k] = v
				}
				for k := range labels {
					f.labels[k] = true
				}
				f.samples = append(f.samples, promSample{labels: labels, value: p.Value()})
			}
		}
	}
	for _, name := range order {
		f := fams[name]
		keys := make([]string, 0, len(f.labels))
		for k := range f.labels {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		desc := prometheus.NewDesc(f.name, f.help, keys, nil)
		seen := map[string]bool{}
		for _, s := range f.samples {
			vals := make([]string, len(keys))
			for i, k := range keys {
				vals[i] = s.labels[k]
			}
			id := strings.Join(vals, "\x00")
			if seen[id] {
				continue // same series from two sources: keep the first
			}
			seen[id] = true
			m, err := prometheus.NewConstMetric(desc, f.typ, s.value, vals...)
			if err != nil {
				ch <- prometheus.NewInvalidMetric(desc, err)
				continue
			}
			ch <- m
		}
	}
}

// promName turns a metric name into a Prometheus one. In OTel naming
// mode, units come back as suffixes and counters get _total, as the
// OpenTelemetry-to-Prometheus conventions say.
func (c *promCollector) promName(m otelmap.Metric) string {
	name := promLabel(m.Name)
	if c.naming == otelmap.NamingOTel {
		if s := promUnits[m.Unit]; s != "" && !strings.HasSuffix(name, "_"+s) {
			name += "_" + s
		}
		if m.Kind == otelmap.Sum && m.Monotonic && !strings.HasSuffix(name, "_total") {
			name += "_total"
		}
	}
	return name
}

var promUnits = map[string]string{
	"s": "seconds", "ms": "milliseconds", "us": "microseconds", "ns": "nanoseconds",
	"By": "bytes", "1": "ratio", "%": "percent",
}

// promLabel replaces characters Prometheus' classic names do not allow.
func promLabel(s string) string {
	b := []byte(s)
	for i, c := range b {
		ok := c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || (i > 0 && c >= '0' && c <= '9')
		if !ok {
			b[i] = '_'
		}
	}
	return string(b)
}
