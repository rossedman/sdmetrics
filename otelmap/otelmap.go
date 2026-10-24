// Package otelmap maps systemd report metrics to OpenTelemetry.
//
// A Mapper turns each scrape.Result into a Batch: one resource per report
// source, carrying the source's unit and service identity, plus one
// resource for sdmetrics' own self-metrics. Batches are neutral; ToSDK
// converts them for the OpenTelemetry Go SDK's exporters, and the
// Collector receiver converts them to pdata.
//
// # Mapping
//
//	systemd type   OpenTelemetry
//	gauge          Gauge
//	counter        Sum, monotonic, cumulative; start time is the first observation
//	string         Gauge = 1, with the string in the systemd.value attribute
//
// A value's fields become point attributes, and its object, if any,
// becomes the systemd.object attribute. Families without a known type
// never reach the mapper: the scraper drops and counts them.
//
// # Resource attributes
//
//	host.name, host.id       hostname, /etc/machine-id
//	systemd.report.source    the socket's file name
//	systemd.unit.name        the unit owning the socket, from its peer credentials and cgroup
//	process.pid              the socket's peer PID
//	service.name             the unit without ".service" ("systemd" for init.scope), else the source name
//
// The systemd.* names are proposals in OpenTelemetry semantic-convention
// style; they are not yet part of the conventions.
package otelmap

import (
	"errors"
	"io/fs"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rossedman/sdmetrics/scrape"
)

// Attribute keys.
const (
	AttrHostName     = "host.name"
	AttrHostID       = "host.id"
	AttrSource       = "systemd.report.source"
	AttrUnit         = "systemd.unit.name"
	AttrPID          = "process.pid"
	AttrServiceName  = "service.name"
	AttrObject       = "systemd.object"
	AttrValue        = "systemd.value"
	AttrDropReason   = "reason"
	AttrUnitProblem  = "reason"
	ScopeName        = "github.com/rossedman/sdmetrics"
	selfMetricPrefix = "sdmetrics."
)

// Naming selects how family names become metric names.
type Naming int

const (
	// NamingRaw keeps systemd's family names: io.systemd.Manager.NRestarts.
	NamingRaw Naming = iota
	// NamingOTel converts names to OpenTelemetry style: lower-case,
	// dot-separated namespaces and snake_case names, io.systemd. shortened
	// to systemd., unit suffixes such as Seconds or Bytes moved into the
	// unit field, and a Total suffix dropped:
	// io.systemd.Manager.UnitsByTypeTotal becomes systemd.manager.units_by_type,
	// io.example.Api.LatencySeconds becomes io.example.api.latency (unit s).
	NamingOTel
)

// Options configure a Mapper.
type Options struct {
	Naming Naming
	// Resource holds attributes added to every resource, such as
	// deployment.environment.
	Resource map[string]string
	// ServiceNames overrides service.name per source name.
	ServiceNames map[string]string
	// Units sets systemd.unit.name per source name, for sources whose unit
	// the scraper cannot resolve, such as socket-activated ones.
	Units map[string]string
	// HostName and HostID default to the hostname and /etc/machine-id.
	HostName, HostID string
	// ScopeVersion is the instrumentation scope version reported.
	ScopeVersion string
}

// Batch is the mapped form of one scrape.
type Batch struct {
	Time      time.Time
	Resources []Resource
}

// Resource is one source's metrics, or the self-metrics.
type Resource struct {
	// Source is the report source name, or "" for the self-metrics.
	Source     string
	Attributes []Attribute
	Metrics    []Metric
}

// Attribute is a string or integer attribute.
type Attribute struct {
	Key   string
	Str   string
	Int   int64
	IsInt bool
}

// String returns the attribute value as text.
func (a Attribute) String() string {
	if a.IsInt {
		return strconv.FormatInt(a.Int, 10)
	}
	return a.Str
}

// MetricKind is an OpenTelemetry metric data type.
type MetricKind int

const (
	// Gauge is an OpenTelemetry Gauge.
	Gauge MetricKind = iota
	// Sum is a cumulative OpenTelemetry Sum.
	Sum
)

// Metric is one metric and its points.
type Metric struct {
	Name, Description, Unit string
	Kind                    MetricKind
	Monotonic               bool // for Sum
	// Family is the systemd family name, or "" for self-metrics.
	Family string
	Points []Point
}

// Point is one data point.
type Point struct {
	Attributes  []Attribute
	Start, Time time.Time
	IsInt       bool
	Int         int64
	Float       float64
}

// Value returns the point's value as float64.
func (p Point) Value() float64 {
	if p.IsInt {
		return float64(p.Int)
	}
	return p.Float
}

// Mapper maps scrape results. It keeps counter start times and
// self-metric totals between scrapes, so use one Mapper per pipeline. It
// is safe for concurrent use.
type Mapper struct {
	opts    Options
	created time.Time

	mu      sync.Mutex
	gen     uint64
	starts  map[string]*startInfo
	dropped map[[2]string]int64 // {source, reason} -> total
}

type startInfo struct {
	start time.Time
	last  float64
	pid   int32
	gen   uint64
}

// keep start times for series unseen for this many scrapes, so a source
// that fails briefly does not reset its counters.
const startTTL = 10

// NewMapper returns a Mapper.
func NewMapper(opts Options) *Mapper {
	if opts.HostName == "" {
		opts.HostName, _ = os.Hostname()
	}
	if opts.HostID == "" {
		opts.HostID = machineID()
	}
	return &Mapper{opts: opts, created: time.Now(), starts: map[string]*startInfo{}, dropped: map[[2]string]int64{}}
}

// machineID returns the host's machine ID, or "" if it cannot be read.
func machineID() string {
	for _, p := range []string{"/etc/machine-id", "/var/lib/dbus/machine-id"} {
		if b, err := os.ReadFile(p); err == nil {
			if id := strings.TrimSpace(string(b)); id != "" {
				return id
			}
		}
	}
	return ""
}

// str returns a string attribute.
func str(k, v string) Attribute { return Attribute{Key: k, Str: v} }

// num returns an integer attribute.
func num(k string, v int64) Attribute { return Attribute{Key: k, Int: v, IsInt: true} }

// hostAttributes are on every resource.
func (m *Mapper) hostAttributes() []Attribute {
	var out []Attribute
	if m.opts.HostName != "" {
		out = append(out, str(AttrHostName, m.opts.HostName))
	}
	if m.opts.HostID != "" {
		out = append(out, str(AttrHostID, m.opts.HostID))
	}
	keys := make([]string, 0, len(m.opts.Resource))
	for k := range m.opts.Resource {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out = append(out, str(k, m.opts.Resource[k]))
	}
	return out
}

// ResourceAttributes returns the resource attributes for a source.
func (m *Mapper) ResourceAttributes(src *scrape.Source) []Attribute {
	attrs := m.hostAttributes()
	attrs = append(attrs, str(AttrSource, src.Name))
	unit := src.Unit
	if u, ok := m.opts.Units[src.Name]; ok {
		unit = u
	}
	if unit != "" {
		attrs = append(attrs, str(AttrUnit, unit))
	}
	if src.Peer != nil && src.Peer.PID > 0 && !src.Activated && src.Unit != "" {
		// A PID is only meaningful when it is the provider's own, not the
		// service manager's for a socket-activated source.
		attrs = append(attrs, num(AttrPID, int64(src.Peer.PID)))
	}
	attrs = append(attrs, str(AttrServiceName, m.serviceName(src.Name, unit)))
	return attrs
}

// serviceName returns the service.name for a source: the configured override,
// else one derived from its unit, else the unit or source name.
func (m *Mapper) serviceName(source, unit string) string {
	if s, ok := m.opts.ServiceNames[source]; ok {
		return s
	}
	switch {
	case unit == "init.scope":
		return "systemd"
	case strings.HasSuffix(unit, ".service"), strings.HasSuffix(unit, ".socket"):
		name := strings.TrimSuffix(strings.TrimSuffix(unit, ".service"), ".socket")
		// Template instances: getty@tty1 is the getty service.
		if i := strings.IndexByte(name, '@'); i > 0 {
			name = name[:i]
		}
		return name
	case unit != "":
		return unit
	}
	return source
}

// Map maps one scrape.
func (m *Mapper) Map(res *scrape.Result) Batch {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gen++
	now := res.Time.Add(res.Duration)
	b := Batch{Time: now}
	for i := range res.Sources {
		src := &res.Sources[i]
		r := Resource{Source: src.Name, Attributes: m.ResourceAttributes(src)}
		for _, f := range src.Families {
			if len(f.Metrics) == 0 {
				continue
			}
			r.Metrics = append(r.Metrics, m.family(src, &f, now))
		}
		if len(r.Metrics) > 0 {
			b.Resources = append(b.Resources, r)
		}
		for reason, n := range map[string]int{"invalid": src.Invalid, "untyped": src.Untyped, "truncated": src.Truncated} {
			if n > 0 {
				m.dropped[[2]string{src.Name, reason}] += int64(n)
			}
		}
	}
	for k, s := range m.starts {
		if m.gen-s.gen > startTTL {
			delete(m.starts, k)
		}
	}
	b.Resources = append(b.Resources, m.self(res, now))
	return b
}

// family maps one scrape family to a metric, with counters as cumulative sums
// and everything else as gauges.
func (m *Mapper) family(src *scrape.Source, f *scrape.Family, now time.Time) Metric {
	name, unit := f.Name, ""
	if m.opts.Naming == NamingOTel {
		name, unit = OTelName(f.Name)
	}
	out := Metric{Name: name, Description: f.Description, Unit: unit, Family: f.Name}
	switch f.Type {
	case scrape.TypeCounter:
		out.Kind, out.Monotonic = Sum, true
	default:
		out.Kind = Gauge
	}
	out.Points = make([]Point, 0, len(f.Metrics))
	for _, v := range f.Metrics {
		p := Point{Time: now, Attributes: pointAttributes(&v, f.Type)}
		switch {
		case f.Type == scrape.TypeString:
			p.IsInt, p.Int = true, 1
		case v.Value.Kind == scrape.KindInt:
			p.IsInt, p.Int = true, v.Value.Int
		default:
			p.Float = v.Value.Float
		}
		if out.Kind == Sum {
			p.Start = m.start(src, f.Name, p, now)
		}
		out.Points = append(out.Points, p)
	}
	return out
}

// pointAttributes returns the attributes of one value: its fields, its object,
// and for string families the string itself.
func pointAttributes(v *scrape.Metric, t scrape.Type) []Attribute {
	attrs := make([]Attribute, 0, len(v.Fields)+2)
	keys := make([]string, 0, len(v.Fields))
	for k := range v.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		attrs = append(attrs, str(k, v.Fields[k]))
	}
	if v.Object != "" {
		attrs = append(attrs, str(AttrObject, v.Object))
	}
	if t == scrape.TypeString {
		attrs = append(attrs, str(AttrValue, v.Value.Str))
	}
	return attrs
}

// start returns the start time of a counter point, resetting it when the
// counter goes down or the source's process changes.
func (m *Mapper) start(src *scrape.Source, family string, p Point, now time.Time) time.Time {
	var key strings.Builder
	key.WriteString(src.Name)
	key.WriteByte(0)
	key.WriteString(family)
	for _, a := range p.Attributes {
		key.WriteByte(0)
		key.WriteString(a.Key)
		key.WriteByte('=')
		key.WriteString(a.Str)
	}
	var pid int32
	if src.Peer != nil {
		pid = src.Peer.PID
	}
	k := key.String()
	s, ok := m.starts[k]
	switch {
	case !ok:
		s = &startInfo{start: now}
		m.starts[k] = s
	case p.Value() < s.last || pid != s.pid:
		s.start = now
	}
	s.last, s.pid, s.gen = p.Value(), pid, m.gen
	return s.start
}

// self returns the resource holding the exporter's own metrics about the
// scrape in res.
func (m *Mapper) self(res *scrape.Result, now time.Time) Resource {
	r := Resource{Attributes: append(m.hostAttributes(), str(AttrServiceName, "sdmetrics"))}
	var failed int64
	up := Metric{Name: selfMetricPrefix + "source.up", Description: "1 if the source was scraped without error, else 0", Kind: Gauge}
	dur := Metric{Name: selfMetricPrefix + "source.scrape.duration", Description: "Time spent scraping the source", Unit: "s", Kind: Gauge}
	resolved := Metric{Name: selfMetricPrefix + "source.unit.resolved", Description: "1 if the source's unit was resolved, else 0 (reason says why)", Kind: Gauge}
	for i := range res.Sources {
		src := &res.Sources[i]
		attrs := []Attribute{str(AttrSource, src.Name)}
		ok := int64(1)
		if src.Err != nil {
			ok = 0
			failed++
		}
		up.Points = append(up.Points, Point{Attributes: attrs, Time: now, IsInt: true, Int: ok})
		dur.Points = append(dur.Points, Point{Attributes: attrs, Time: now, Float: src.Duration.Seconds()})
		_, overridden := m.opts.Units[src.Name]
		rp := Point{Attributes: attrs, Time: now, IsInt: true, Int: 1}
		if src.Unit == "" && !overridden {
			rp.Int = 0
			rp.Attributes = []Attribute{attrs[0], str(AttrUnitProblem, unitProblem(src))}
		}
		resolved.Points = append(resolved.Points, rp)
	}
	r.Metrics = append(r.Metrics,
		Metric{Name: selfMetricPrefix + "sources", Description: "Report sources found", Unit: "{source}", Kind: Gauge,
			Points: []Point{{Time: now, IsInt: true, Int: int64(len(res.Sources))}}},
		Metric{Name: selfMetricPrefix + "sources.failed", Description: "Report sources that could not be scraped", Unit: "{source}", Kind: Gauge,
			Points: []Point{{Time: now, IsInt: true, Int: failed}}},
		Metric{Name: selfMetricPrefix + "scrape.duration", Description: "Time spent scraping all sources", Unit: "s", Kind: Gauge,
			Points: []Point{{Time: now, Float: res.Duration.Seconds()}}},
	)
	if len(res.Sources) > 0 {
		r.Metrics = append(r.Metrics, up, dur, resolved)
	}
	dropped := Metric{Name: selfMetricPrefix + "metrics.dropped", Description: "Metric values dropped: invalid name or value, unknown type, or over the per-source limit", Unit: "{value}", Kind: Sum, Monotonic: true}
	keys := make([][2]string, 0, len(m.dropped))
	for k := range m.dropped {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i][0]+keys[i][1] < keys[j][0]+keys[j][1] })
	for _, k := range keys {
		dropped.Points = append(dropped.Points, Point{
			Attributes: []Attribute{str(AttrSource, k[0]), str(AttrDropReason, k[1])},
			Start:      m.created, Time: now, IsInt: true, Int: m.dropped[k],
		})
	}
	if len(dropped.Points) > 0 {
		r.Metrics = append(r.Metrics, dropped)
	}
	return r
}

// unitProblem returns the reason code for why a source's unit was not resolved.
func unitProblem(src *scrape.Source) string {
	err := src.UnitErr
	switch {
	case err == nil && src.Err != nil:
		return "scrape_failed"
	case err == nil:
		return "unknown"
	case errors.Is(err, scrape.ErrActivated):
		return "activated"
	case errors.Is(err, scrape.ErrNoUnit):
		return "no_unit"
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, fs.ErrPermission):
		return "proc_unreadable"
	}
	return "peer_unavailable"
}
