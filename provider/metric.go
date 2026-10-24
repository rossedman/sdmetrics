package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/rossedman/sdvarlink/systemd/v260/metrics"

	"github.com/rossedman/sdmetrics/internal/names"
)

// MetricOption configures a metric at registration.
type MetricOption func(*metricConfig)

type metricConfig struct {
	help   string
	labels []string
}

// Help sets the family's description, which Describe reports.
func Help(text string) MetricOption { return func(c *metricConfig) { c.help = text } }

// Labels declares the metric's label names. Each series then needs a
// value per label, given to With. Label names follow the metric naming
// rules.
func Labels(names ...string) MetricOption {
	return func(c *metricConfig) { c.labels = append(c.labels, names...) }
}

// newFamily applies opts and registers a family called name, panicking on an
// invalid or duplicate metric or label name.
func (p *Provider) newFamily(name string, t Type, opts []MetricOption) *family {
	var cfg metricConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	seen := map[string]bool{}
	for _, l := range cfg.labels {
		if !names.ValidField(l) {
			panic(fmt.Sprintf("provider: metric %q: invalid label name %q", name, l))
		}
		if seen[l] {
			panic(fmt.Sprintf("provider: metric %q: label %q declared twice", name, l))
		}
		seen[l] = true
	}
	f := &family{name: p.claim(name), help: cfg.help, typ: t, labels: cfg.labels}
	f.root = &series{}
	f.index.Store(&map[uint64][]*series{})
	f.ordered.Store(&[]*series{})
	return f
}

// family is a registered metric: its description and its series.
type family struct {
	name   string
	help   string
	typ    Type
	labels []string

	root *series // the series without object or labels

	mu      sync.Mutex // serializes series creation and deletion
	index   atomic.Pointer[map[uint64][]*series]
	ordered atomic.Pointer[[]*series] // in creation order, for stable output
}

// series holds one value. Gauges keep float64 bits, counters a uint64.
type series struct {
	object string
	values []string
	fields map[string]string // label name -> value, built once
	bits   atomic.Uint64
	str    atomic.Pointer[string]
	used   atomic.Bool // the root series is published once written
}

// seriesHash hashes an object and label values into a series index key.
func seriesHash(object string, values []string) uint64 {
	// FNV-1a with a separator after each part, so ("ab","") and ("a","b")
	// differ.
	const offset, prime = 14695981039346656037, 1099511628211
	h := uint64(offset)
	mix := func(s string) {
		for i := 0; i < len(s); i++ {
			h ^= uint64(s[i])
			h *= prime
		}
		h ^= 0xff
		h *= prime
	}
	mix(object)
	for _, v := range values {
		mix(v)
	}
	return h
}

// get returns the series for object and values, creating it if needed.
// Lookups take no lock and do not allocate.
func (f *family) get(object string, values []string) *series {
	if len(values) != 0 && len(values) != len(f.labels) {
		panic(fmt.Sprintf("provider: metric %q: got %d label values for %d labels", f.name, len(values), len(f.labels)))
	}
	if object == "" && len(values) == 0 {
		return f.root
	}
	h := seriesHash(object, values)
	for _, s := range (*f.index.Load())[h] {
		if s.object == object && slices.Equal(s.values, values) {
			return s
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	old := *f.index.Load()
	for _, s := range old[h] {
		if s.object == object && slices.Equal(s.values, values) {
			return s
		}
	}
	s := &series{object: object, values: slices.Clone(values)}
	if len(values) > 0 {
		s.fields = make(map[string]string, len(values))
		for i, l := range f.labels {
			s.fields[l] = values[i]
		}
	}
	next := make(map[uint64][]*series, len(old)+1)
	for k, v := range old {
		next[k] = v
	}
	next[h] = append(slices.Clip(old[h]), s)
	f.index.Store(&next)
	list := *f.ordered.Load()
	f.ordered.Store(ptr(append(slices.Clip(list), s)))
	return s
}

// remove deletes a series; later lookups create a fresh one.
func (f *family) remove(s *series) {
	if s == f.root {
		s.used.Store(false)
		s.bits.Store(0)
		s.str.Store(nil)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	h := seriesHash(s.object, s.values)
	old := *f.index.Load()
	next := make(map[uint64][]*series, len(old))
	for k, v := range old {
		next[k] = v
	}
	bucket := slices.DeleteFunc(slices.Clone(old[h]), func(x *series) bool { return x == s })
	if len(bucket) == 0 {
		delete(next, h)
	} else {
		next[h] = bucket
	}
	f.index.Store(&next)
	f.ordered.Store(ptr(slices.DeleteFunc(slices.Clone(*f.ordered.Load()), func(x *series) bool { return x == s })))
}

// visible reports whether a series is published. The root series of a
// family without labels always is, so a plain counter reads 0 before its
// first increment; every other series is published once written, so
// intermediate handles such as c.On(x) before .With(y) never appear.
func (f *family) visible(s *series) bool {
	return (s == f.root && len(f.labels) == 0) || s.used.Load()
}

// describe emits the family's description.
func (f *family) describe(_ context.Context, emit func(metrics.DescribeOut) error) error {
	return emit(metrics.DescribeOut{Name: f.name, Description: f.help, Type: metrics.MetricFamilyType(f.typ)})
}

// list emits every visible series of the family.
func (f *family) list(_ context.Context, emit func(metrics.ListOut) error) error {
	send := func(s *series) error {
		var v json.RawMessage
		switch f.typ {
		case TypeCounter:
			v, _ = encodeValue(TypeCounter, s.bits.Load())
		case TypeGauge:
			var ok bool
			if v, ok = encodeFloat(math.Float64frombits(s.bits.Load())); !ok {
				return nil // NaN or Inf: no JSON form
			}
		case TypeString:
			str := s.str.Load()
			if str == nil {
				return nil
			}
			v, _ = json.Marshal(*str)
		}
		return emit(listOut(f.name, s.object, s.fields, v))
	}
	if f.visible(f.root) {
		if err := send(f.root); err != nil {
			return err
		}
	}
	for _, s := range *f.ordered.Load() {
		if !f.visible(s) {
			continue
		}
		if err := send(s); err != nil {
			return err
		}
	}
	return nil
}

// Gauge is a value that can go up and down. The zero series (no object,
// no labels) is the Gauge itself; With and On return other series.
type Gauge struct {
	f *family
	s *series
}

// Gauge registers a gauge. It panics if name is invalid or taken.
func (p *Provider) Gauge(name string, opts ...MetricOption) Gauge {
	f := p.newFamily(name, TypeGauge, opts)
	p.add(f)
	return Gauge{f: f, s: f.root}
}

// With returns the series for the given label values, one per declared
// label, keeping the receiver's object.
func (g Gauge) With(values ...string) Gauge {
	return Gauge{f: g.f, s: g.f.get(g.s.object, values)}
}

// On returns the series about object, such as a unit or interface name,
// keeping the receiver's label values.
func (g Gauge) On(object string) Gauge {
	return Gauge{f: g.f, s: g.f.get(object, g.s.values)}
}

// Set sets the value.
func (g Gauge) Set(v float64) {
	g.s.bits.Store(math.Float64bits(v))
	g.s.used.Store(true)
}

// Add adds delta, which may be negative.
func (g Gauge) Add(delta float64) {
	for {
		old := g.s.bits.Load()
		if g.s.bits.CompareAndSwap(old, math.Float64bits(math.Float64frombits(old)+delta)) {
			break
		}
	}
	g.s.used.Store(true)
}

// Inc adds one.
func (g Gauge) Inc() { g.Add(1) }

// Dec subtracts one.
func (g Gauge) Dec() { g.Add(-1) }

// Value returns the current value.
func (g Gauge) Value() float64 { return math.Float64frombits(g.s.bits.Load()) }

// Delete stops publishing the series, for example when the object it is
// about goes away. Using the handle afterwards does not bring it back;
// call With or On again. On the metric's own handle, Delete resets the
// value instead; a metric without labels is always published.
func (g Gauge) Delete() { g.f.remove(g.s) }

// Counter is a monotonically increasing unsigned integer, published as a
// counter: consumers compute rates from it and treat a decrease as a
// restart.
type Counter struct {
	f *family
	s *series
}

// Counter registers a counter. It panics if name is invalid or taken.
func (p *Provider) Counter(name string, opts ...MetricOption) Counter {
	f := p.newFamily(name, TypeCounter, opts)
	p.add(f)
	return Counter{f: f, s: f.root}
}

// With returns the series for the given label values.
func (c Counter) With(values ...string) Counter {
	return Counter{f: c.f, s: c.f.get(c.s.object, values)}
}

// On returns the series about object.
func (c Counter) On(object string) Counter {
	return Counter{f: c.f, s: c.f.get(object, c.s.values)}
}

// Inc adds one.
func (c Counter) Inc() { c.Add(1) }

// Add adds n.
func (c Counter) Add(n uint64) {
	c.s.bits.Add(n)
	c.s.used.Store(true)
}

// Value returns the current value.
func (c Counter) Value() uint64 { return c.s.bits.Load() }

// Delete stops publishing the series.
func (c Counter) Delete() { c.f.remove(c.s) }

// String is a string value such as a state or a version. Series without
// a value are not published.
type String struct {
	f *family
	s *series
}

// String registers a string metric. It panics if name is invalid or
// taken.
func (p *Provider) String(name string, opts ...MetricOption) String {
	f := p.newFamily(name, TypeString, opts)
	p.add(f)
	return String{f: f, s: f.root}
}

// With returns the series for the given label values.
func (s String) With(values ...string) String {
	return String{f: s.f, s: s.f.get(s.s.object, values)}
}

// On returns the series about object.
func (s String) On(object string) String {
	return String{f: s.f, s: s.f.get(object, s.s.values)}
}

// Set sets the value. Unlike numeric updates, Set allocates.
func (s String) Set(v string) {
	s.s.str.Store(&v)
	s.s.used.Store(true)
}

// Value returns the current value, or "" if none was set.
func (s String) Value() string {
	if v := s.s.str.Load(); v != nil {
		return *v
	}
	return ""
}

// Delete stops publishing the series.
func (s String) Delete() { s.f.remove(s.s) }

// funcFamily is a metric whose single value is computed on each read.
type funcFamily struct {
	name, help string
	typ        Type
	read       func() any
}

// describe emits the metric's description.
func (f *funcFamily) describe(_ context.Context, emit func(metrics.DescribeOut) error) error {
	return emit(metrics.DescribeOut{Name: f.name, Description: f.help, Type: metrics.MetricFamilyType(f.typ)})
}

// list emits the metric's current value, or nothing if it cannot be encoded.
func (f *funcFamily) list(_ context.Context, emit func(metrics.ListOut) error) error {
	v, ok := encodeValue(f.typ, f.read())
	if !ok {
		return nil
	}
	return emit(listOut(f.name, "", nil, v))
}

// addFunc registers a metric whose value is read from read, panicking if
// opts declare labels.
func (p *Provider) addFunc(name string, t Type, read func() any, opts []MetricOption) {
	var cfg metricConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	if len(cfg.labels) > 0 {
		panic(fmt.Sprintf("provider: %q: func metrics take no labels; implement a Collector instead", name))
	}
	p.add(&funcFamily{name: p.claim(name), help: cfg.help, typ: t, read: read})
}

// GaugeFunc registers a gauge whose value fn computes on each read, such
// as a cache size. fn must be safe to call concurrently.
func (p *Provider) GaugeFunc(name string, fn func() float64, opts ...MetricOption) {
	p.addFunc(name, TypeGauge, func() any { return fn() }, opts)
}

// CounterFunc registers a counter whose value fn computes on each read.
func (p *Provider) CounterFunc(name string, fn func() uint64, opts ...MetricOption) {
	p.addFunc(name, TypeCounter, func() any { return fn() }, opts)
}

// StringFunc registers a string metric whose value fn computes on each
// read. An empty string publishes no value.
func (p *Provider) StringFunc(name string, fn func() string, opts ...MetricOption) {
	p.addFunc(name, TypeString, func() any {
		if v := fn(); v != "" {
			return v
		}
		return nil
	}, opts)
}
