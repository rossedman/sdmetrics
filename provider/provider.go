package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rossedman/sdvarlink/systemd/v260/metrics"
	"github.com/rossedman/sdvarlink/varlink"

	"github.com/rossedman/sdmetrics/internal/names"
)

// DefaultDir is where systemd-report looks for system-wide sources.
const DefaultDir = "/run/systemd/report"

// Type is a metric family type, as io.systemd.Metrics defines it.
type Type string

// Metric family types.
const (
	// TypeCounter is a monotonically increasing value.
	TypeCounter Type = "counter"
	// TypeGauge is a value that can go up and down.
	TypeGauge Type = "gauge"
	// TypeString is a string value, such as a state.
	TypeString Type = "string"
)

// Provider serves metrics on a report socket. Register metrics with
// Gauge, Counter, String and their Func variants, or add a Collector, then
// call Serve. Registration may continue while serving. A Provider is safe
// for concurrent use.
type Provider struct {
	name string
	cfg  config

	mu       sync.Mutex // serializes registration
	families atomic.Pointer[[]source]
	seen     map[string]bool // registered family names

	served atomic.Bool
}

// source is a registered metric or collector.
type source interface {
	// describe reports the families the source publishes.
	describe(ctx context.Context, emit func(metrics.DescribeOut) error) error
	// list reports their current values.
	list(ctx context.Context, emit func(metrics.ListOut) error) error
}

type config struct {
	dir        string
	path       string
	mode       os.FileMode
	uid, gid   int
	group      string
	activation bool
	listener   net.Listener
	logger     *slog.Logger
	info       varlink.ServerInfo
	idle       time.Duration
	maxConns   int
}

// Option configures a Provider.
type Option func(*config)

// WithDir sets the directory the socket is created in (default
// /run/systemd/report). Per-user sources live in
// $XDG_RUNTIME_DIR/systemd/report, where systemd-report --user looks.
func WithDir(dir string) Option { return func(c *config) { c.dir = dir } }

// WithSocketPath sets the socket's full path, overriding WithDir. The
// file name should still be the provider name: consumers drop families
// that are not named after their socket.
func WithSocketPath(path string) Option { return func(c *config) { c.path = path } }

// WithSocketMode sets the socket's permissions (default 0660). systemd's
// own sources use 0666, so any local user can read them.
func WithSocketMode(mode os.FileMode) Option { return func(c *config) { c.mode = mode } }

// WithSocketOwner sets the socket's owner and group IDs; -1 leaves one
// unchanged.
func WithSocketOwner(uid, gid int) Option { return func(c *config) { c.uid, c.gid = uid, gid } }

// WithSocketGroup sets the socket's group by name, for example the group
// sdmetrics-exporter runs with.
func WithSocketGroup(group string) Option { return func(c *config) { c.group = group } }

// FromActivation adopts a socket passed by systemd socket activation
// instead of binding one. With several sockets passed, the one whose
// FileDescriptorName= is the provider name is used. When the process was
// not socket-activated, Serve binds the socket as usual.
func FromActivation() Option { return func(c *config) { c.activation = true } }

// WithListener serves on l instead of binding a socket. The provider
// closes l when Serve returns.
func WithListener(l net.Listener) Option { return func(c *config) { c.listener = l } }

// WithLogger sets the logger for serving errors (default slog.Default()).
func WithLogger(l *slog.Logger) Option { return func(c *config) { c.logger = l } }

// WithServerInfo sets what org.varlink.service.GetInfo reports. By
// default the vendor is "sdmetrics", the product the provider name and
// the version the main module's.
func WithServerInfo(info varlink.ServerInfo) Option { return func(c *config) { c.info = info } }

// WithIdleTimeout closes consumer connections idle for d (default one
// minute).
func WithIdleTimeout(d time.Duration) Option { return func(c *config) { c.idle = d } }

// WithMaxConnections limits concurrent consumer connections (default 16).
func WithMaxConnections(n int) Option { return func(c *config) { c.maxConns = n } }

// New returns a provider named name, a Varlink interface name such as
// io.example.Queue. It does not bind the socket; Serve does.
func New(name string, opts ...Option) (*Provider, error) {
	if !names.ValidSource(name) {
		return nil, fmt.Errorf("provider: invalid name %q: want a reverse-domain Varlink interface name such as io.example.Queue", name)
	}
	cfg := config{
		dir:      DefaultDir,
		mode:     0o660,
		uid:      -1,
		gid:      -1,
		logger:   slog.Default(),
		idle:     time.Minute,
		maxConns: 16,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.path == "" {
		cfg.path = filepath.Join(cfg.dir, name)
	}
	if cfg.group != "" {
		g, err := user.LookupGroup(cfg.group)
		if err != nil {
			return nil, fmt.Errorf("provider: socket group: %w", err)
		}
		gid, err := strconv.Atoi(g.Gid)
		if err != nil {
			return nil, fmt.Errorf("provider: socket group %q: %w", cfg.group, err)
		}
		cfg.gid = gid
	}
	if cfg.info.Vendor == "" {
		cfg.info.Vendor = "sdmetrics"
	}
	if cfg.info.Product == "" {
		cfg.info.Product = name
	}
	if cfg.info.Version == "" {
		cfg.info.Version = mainVersion()
	}
	if cfg.info.URL == "" {
		cfg.info.URL = "https://github.com/rossedman/sdmetrics"
	}
	p := &Provider{name: name, cfg: cfg, seen: map[string]bool{}}
	p.families.Store(&[]source{})
	return p, nil
}

func mainVersion() string {
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" {
		return bi.Main.Version
	}
	return "(devel)"
}

// Name returns the provider name.
func (p *Provider) Name() string { return p.name }

// Path returns the socket path Serve binds when not socket-activated.
func (p *Provider) Path() string { return p.cfg.path }

// family returns the full family name for a metric name, or panics if
// consumers would drop it or it is registered twice.
func (p *Provider) claim(name string) string {
	full := p.name + "." + name
	if !names.BelongsTo(full, p.name) {
		panic(fmt.Sprintf("provider: invalid metric name %q: want letters, digits and single underscores, starting with a letter", name))
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.seen[full] {
		panic(fmt.Sprintf("provider: metric %q registered twice", full))
	}
	p.seen[full] = true
	return full
}

func (p *Provider) add(s source) {
	p.mu.Lock()
	defer p.mu.Unlock()
	old := *p.families.Load()
	next := make([]source, len(old), len(old)+1)
	copy(next, old)
	p.families.Store(ptr(append(next, s)))
}

func ptr[T any](v T) *T { return &v }

// Collector is a source of metric families computed on each read, for
// bridging other instrumentation libraries. Collect is called for every
// List and Describe call, possibly concurrently.
type Collector interface {
	Collect(ctx context.Context) ([]Family, error)
}

// Family is one metric family reported by a Collector.
type Family struct {
	// Name is the metric name relative to the provider, such as
	// "RequestsTotal"; the provider prefixes its own name. Names that are
	// not valid field names are dropped and logged.
	Name        string
	Description string
	Type        Type
	Samples     []Sample
}

// Sample is one value of a family.
type Sample struct {
	// Object names what the value is about, such as a unit; optional.
	Object string
	// Fields distinguish the samples of a family, like labels.
	Fields map[string]string
	// Value is a number (any integer or float type) for counters and
	// gauges, and a string for TypeString.
	Value any
}

// Register adds a Collector. Its families are checked on each read; a
// family whose name collides with a registered metric is dropped.
func (p *Provider) Register(c Collector) {
	p.add(&collectorSource{p: p, c: c})
}

type collectorSource struct {
	p      *Provider
	c      Collector
	warned sync.Map // family name -> struct{}
}

func (s *collectorSource) collect(ctx context.Context) ([]Family, []string) {
	fams, err := s.c.Collect(ctx)
	if err != nil {
		s.p.cfg.logger.Warn("provider: collector failed", "provider", s.p.name, "err", err)
	}
	full := make([]string, len(fams))
	s.p.mu.Lock()
	defer s.p.mu.Unlock()
	for i, f := range fams {
		name := s.p.name + "." + f.Name
		switch {
		case !names.BelongsTo(name, s.p.name):
			s.warnOnce(name, "invalid family name")
		case s.p.seen[name]:
			s.warnOnce(name, "family name collides with a registered metric")
		case f.Type != TypeCounter && f.Type != TypeGauge && f.Type != TypeString:
			s.warnOnce(name, "unknown family type "+string(f.Type))
		default:
			full[i] = name
		}
	}
	return fams, full
}

func (s *collectorSource) warnOnce(name, why string) {
	if _, dup := s.warned.LoadOrStore(name, struct{}{}); !dup {
		s.p.cfg.logger.Warn("provider: dropping collector family", "provider", s.p.name, "family", name, "reason", why)
	}
}

func (s *collectorSource) describe(ctx context.Context, emit func(metrics.DescribeOut) error) error {
	fams, full := s.collect(ctx)
	for i, f := range fams {
		if full[i] == "" {
			continue
		}
		if err := emit(metrics.DescribeOut{Name: full[i], Description: f.Description, Type: metrics.MetricFamilyType(f.Type)}); err != nil {
			return err
		}
	}
	return nil
}

func (s *collectorSource) list(ctx context.Context, emit func(metrics.ListOut) error) error {
	fams, full := s.collect(ctx)
	for i, f := range fams {
		if full[i] == "" {
			continue
		}
		for _, smp := range f.Samples {
			v, ok := encodeValue(f.Type, smp.Value)
			if !ok {
				s.warnOnce(full[i], fmt.Sprintf("value %v (%T) does not fit type %s", smp.Value, smp.Value, f.Type))
				continue
			}
			if err := emit(listOut(full[i], smp.Object, smp.Fields, v)); err != nil {
				return err
			}
		}
	}
	return nil
}

// encodeValue returns the JSON for a sample value of type t.
func encodeValue(t Type, v any) (json.RawMessage, bool) {
	if t == TypeString {
		s, ok := v.(string)
		if !ok {
			return nil, false
		}
		b, _ := json.Marshal(s)
		return b, true
	}
	switch n := v.(type) {
	case int:
		return strconv.AppendInt(nil, int64(n), 10), true
	case int8:
		return strconv.AppendInt(nil, int64(n), 10), true
	case int16:
		return strconv.AppendInt(nil, int64(n), 10), true
	case int32:
		return strconv.AppendInt(nil, int64(n), 10), true
	case int64:
		return strconv.AppendInt(nil, n, 10), true
	case uint:
		return strconv.AppendUint(nil, uint64(n), 10), true
	case uint8:
		return strconv.AppendUint(nil, uint64(n), 10), true
	case uint16:
		return strconv.AppendUint(nil, uint64(n), 10), true
	case uint32:
		return strconv.AppendUint(nil, uint64(n), 10), true
	case uint64:
		return strconv.AppendUint(nil, n, 10), true
	case float32:
		return encodeFloat(float64(n))
	case float64:
		return encodeFloat(n)
	}
	return nil, false
}

// encodeFloat formats f as JSON. NaN and infinities have no JSON form,
// so samples holding them are skipped.
func encodeFloat(f float64) (json.RawMessage, bool) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return nil, false
	}
	return strconv.AppendFloat(nil, f, 'g', -1, 64), true
}

func listOut(name, object string, fields map[string]string, value json.RawMessage) metrics.ListOut {
	out := metrics.ListOut{Name: name, Value: value}
	if object != "" {
		out.Object = &object
	}
	if len(fields) > 0 {
		out.Fields, _ = json.Marshal(fields)
	}
	return out
}

// Serve publishes the metrics until ctx ends, then closes the socket and,
// if Serve bound it, removes the file. It returns an error if the socket
// cannot be bound or adopted, or if accepting fails. Serve may be called
// once.
func (p *Provider) Serve(ctx context.Context) error {
	if !p.served.CompareAndSwap(false, true) {
		return errors.New("provider: Serve called twice")
	}
	l, err := p.listen()
	if err != nil {
		return err
	}
	defer l.Close()
	srv := varlink.NewServer(p.cfg.info,
		varlink.WithLogger(p.cfg.logger),
		varlink.WithIdleTimeout(p.cfg.idle),
		varlink.WithMaxConnections(p.cfg.maxConns),
		// Consumers send empty calls; nothing legitimate is large.
		varlink.WithServerMaxFrameSize(64<<10),
		varlink.WithServerMaxFiles(0),
	)
	p.register(srv)
	return srv.Serve(ctx, l)
}

func (p *Provider) listen() (net.Listener, error) {
	if p.cfg.listener != nil {
		return p.cfg.listener, nil
	}
	if p.cfg.activation {
		l, err := varlink.ListenActivated(p.name)
		if err != nil && !errors.Is(err, varlink.ErrNotActivated) {
			// Not named after the provider: take the only socket passed.
			l, err = varlink.ListenActivated("")
		}
		if err == nil {
			return l, nil
		}
		if !errors.Is(err, varlink.ErrNotActivated) {
			return nil, fmt.Errorf("provider: socket activation: %w", err)
		}
	}
	l, err := varlink.Listen(p.cfg.path, varlink.WithSocketMode(p.cfg.mode), varlink.WithSocketOwner(p.cfg.uid, p.cfg.gid))
	if err != nil {
		return nil, fmt.Errorf("provider: %w", err)
	}
	return l, nil
}

// register adds the io.systemd.Metrics handlers to srv.
func (p *Provider) register(srv *varlink.Server) {
	iface := srv.Interface(metrics.InterfaceName, metrics.InterfaceDescription)
	varlink.HandleStream(iface, "List", func(ctx context.Context, _ *varlink.Call, _ struct{}, send func(metrics.ListOut) error) error {
		sent := false
		for _, s := range *p.families.Load() {
			err := s.list(ctx, func(out metrics.ListOut) error {
				sent = true
				return send(out)
			})
			if err != nil {
				return err
			}
		}
		if !sent {
			// As systemd's own providers do for an empty list.
			return &metrics.NoSuchMetricError{}
		}
		return nil
	}, varlink.LastIsFinal())
	varlink.HandleStream(iface, "Describe", func(ctx context.Context, _ *varlink.Call, _ struct{}, send func(metrics.DescribeOut) error) error {
		sent := false
		for _, s := range *p.families.Load() {
			err := s.describe(ctx, func(out metrics.DescribeOut) error {
				sent = true
				return send(out)
			})
			if err != nil {
				return err
			}
		}
		if !sent {
			return &metrics.NoSuchMetricError{}
		}
		return nil
	}, varlink.LastIsFinal())
}
