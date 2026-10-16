// Package scrape collects metrics from every systemd report source on a
// host (systemd v260+).
//
// A source is a socket under /run/systemd/report/ that answers the
// io.systemd.Metrics Varlink interface. Scrape lists the directory, calls
// each socket concurrently with a per-source timeout, resolves the unit
// that owns it from its peer credentials and cgroup, and returns typed
// metric families:
//
//	s := scrape.New(scrape.Options{})
//	res, err := s.Scrape(ctx)
//	if err != nil {
//		return err // the directory could not be read
//	}
//	for _, src := range res.Sources {
//		if src.Err != nil {
//			log.Printf("%s: %v", src.Name, src.Err) // one failing source never fails the batch
//			continue
//		}
//		for _, f := range src.Families {
//			fmt.Println(src.Unit, f.Name, f.Type, len(f.Metrics))
//		}
//	}
//
// The scraper applies the same rules as systemd-report: entries must be
// sockets (or symlinks to them) named like Varlink interfaces, and
// families must be named after the socket they come from. Families that
// break the rules, values of the wrong kind, and families Describe does
// not type are dropped and counted on the Source.
package scrape

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/rossedman/sdvarlink/systemd/v260/metrics"
	"github.com/rossedman/sdvarlink/varlink"

	"github.com/rossedman/sdmetrics/internal/names"
)

// DefaultDir is where system-wide sources live.
const DefaultDir = "/run/systemd/report"

// Defaults for Options.
const (
	DefaultConcurrency  = 8
	DefaultTimeout      = 2 * time.Second
	DefaultMaxFrameSize = 1 << 20
	DefaultMaxMetrics   = 16384
)

// ErrNoReportDir is returned by Scrape when the report directory does not
// exist, as on hosts older than systemd v260.
var ErrNoReportDir = errors.New("scrape: report directory does not exist (systemd v260 or newer required)")

// Options configure a Scraper. The zero value scrapes /run/systemd/report
// with the defaults above.
type Options struct {
	// Dir is the report directory (default /run/systemd/report).
	Dir string
	// Include, if set, limits scraping to sources whose name matches one
	// of these patterns, in path.Match syntax: "io.systemd.*".
	Include []string
	// Exclude skips sources whose name matches one of these patterns.
	Exclude []string
	// Concurrency is how many sources are called at once.
	Concurrency int
	// Timeout bounds the time spent on each source.
	Timeout time.Duration
	// MaxFrameSize is the largest single reply accepted from a source.
	MaxFrameSize int
	// MaxMetrics is the most metric values kept per source; the rest are
	// counted in Source.Truncated.
	MaxMetrics int
	// ProcRoot is where /proc is mounted (default "/proc"), for tests.
	ProcRoot string
	// Logger receives one warning per source and family for dropped data
	// (default slog.Default()).
	Logger *slog.Logger
	// ManagerSocket is the service manager's Varlink socket (default
	// /run/systemd/io.systemd.Manager), asked which unit owns a
	// socket-activated source. Per-user managers listen on
	// $XDG_RUNTIME_DIR/systemd/io.systemd.Manager.
	ManagerSocket string
	// NoActivationLookup disables that lookup.
	NoActivationLookup bool
}

// Scraper scrapes report sources. It is safe for concurrent use.
type Scraper struct {
	opts    Options
	warned  sync.Map // "source\x00family\x00reason" -> struct{}
	sockets socketIndex
}

// New returns a Scraper. Invalid patterns are reported by Scrape.
func New(opts Options) *Scraper {
	if opts.Dir == "" {
		opts.Dir = DefaultDir
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = DefaultConcurrency
	}
	if opts.Timeout <= 0 {
		opts.Timeout = DefaultTimeout
	}
	if opts.MaxFrameSize <= 0 {
		opts.MaxFrameSize = DefaultMaxFrameSize
	}
	if opts.MaxMetrics <= 0 {
		opts.MaxMetrics = DefaultMaxMetrics
	}
	if opts.ProcRoot == "" {
		opts.ProcRoot = "/proc"
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.ManagerSocket == "" {
		opts.ManagerSocket = DefaultManagerSocket
	}
	return &Scraper{opts: opts}
}

// Type is a metric family type.
type Type string

// Family types, as io.systemd.Metrics defines them.
const (
	TypeCounter = Type(metrics.MetricFamilyTypeCounter)
	TypeGauge   = Type(metrics.MetricFamilyTypeGauge)
	TypeString  = Type(metrics.MetricFamilyTypeString)
)

// Result is one scrape of all sources.
type Result struct {
	// Time is when the scrape started.
	Time time.Time
	// Duration is how long the whole scrape took.
	Duration time.Duration
	// Sources are the sources found, sorted by name.
	Sources []Source
}

// Peer identifies the process that created a source's socket.
type Peer struct {
	PID      int32
	UID, GID uint32
}

// Source is the outcome of scraping one socket.
type Source struct {
	// Name is the socket's file name, such as io.systemd.Manager.
	Name string
	// Path is the socket's path.
	Path string
	// Peer holds the socket's peer credentials; nil if unavailable.
	Peer *Peer
	// Unit is the systemd unit owning the source, such as
	// systemd-networkd.service, or "" if it could not be resolved; then
	// UnitErr says why (see ErrActivated).
	Unit    string
	UnitErr error
	// Activated reports that the service manager created the socket
	// (socket activation), so Peer is PID 1's and says nothing about the
	// service. Unit then comes from the socket unit, on systemd v262 and
	// newer: the service it triggers, or the socket unit itself for
	// Accept=yes sockets that start one service per connection.
	Activated bool
	// Families are the metric families, in the order Describe sent them.
	Families []Family
	// Err is set if the source could not be scraped; Families then holds
	// whatever arrived before the error.
	Err error
	// Duration is how long the source took.
	Duration time.Duration
	// Invalid counts values dropped for a bad family name or value.
	Invalid int
	// Untyped counts values dropped because Describe gave their family no
	// known type.
	Untyped int
	// Truncated counts values dropped beyond Options.MaxMetrics.
	Truncated int
}

// Family is a metric family and its values.
type Family struct {
	Name        string
	Description string
	Type        Type
	Metrics     []Metric
}

// Metric is one value of a family.
type Metric struct {
	// Object is what the value is about, such as a unit name; optional.
	Object string
	// Fields distinguish the values of a family, like labels.
	Fields map[string]string
	Value  Value
}

// Scrape scrapes every matching source. It returns an error only if the
// directory cannot be read (ErrNoReportDir if it does not exist) or a
// pattern is malformed; failures of single sources are reported in
// Source.Err.
func (s *Scraper) Scrape(ctx context.Context) (*Result, error) {
	start := time.Now()
	found, err := s.discover()
	if err != nil {
		return nil, err
	}
	res := &Result{Time: start, Sources: make([]Source, len(found))}
	sem := make(chan struct{}, s.opts.Concurrency)
	var wg sync.WaitGroup
	for i, src := range found {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				res.Sources[i] = Source{Name: src.name, Path: src.path, Err: ctx.Err()}
				return
			}
			defer func() { <-sem }()
			res.Sources[i] = s.scrapeOne(ctx, src.name, src.path)
		}()
	}
	wg.Wait()
	if !s.opts.NoActivationLookup {
		s.resolveActivated(ctx, res.Sources)
	}
	res.Duration = time.Since(start)
	return res, nil
}

type entry struct{ name, path string }

// discover lists the report directory as systemd-report does.
func (s *Scraper) discover() ([]entry, error) {
	for _, p := range append(append([]string{}, s.opts.Include...), s.opts.Exclude...) {
		if _, err := path.Match(p, ""); err != nil {
			return nil, fmt.Errorf("scrape: bad pattern %q: %w", p, err)
		}
	}
	des, err := os.ReadDir(s.opts.Dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNoReportDir
	}
	if err != nil {
		return nil, fmt.Errorf("scrape: %w", err)
	}
	var out []entry
	for _, de := range des {
		name := de.Name()
		switch de.Type() {
		case fs.ModeSocket, fs.ModeSymlink:
		default:
			continue
		}
		if !names.ValidSource(name) || !s.wanted(name) {
			continue
		}
		p := filepath.Join(s.opts.Dir, name)
		if de.Type() == fs.ModeSymlink {
			fi, err := os.Stat(p)
			if err != nil || fi.Mode()&fs.ModeSocket == 0 {
				continue
			}
		}
		out = append(out, entry{name: name, path: p})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, nil
}

func (s *Scraper) wanted(name string) bool {
	match := func(pats []string) bool {
		for _, p := range pats {
			if ok, _ := path.Match(p, name); ok {
				return true
			}
		}
		return false
	}
	if len(s.opts.Include) > 0 && !match(s.opts.Include) {
		return false
	}
	return !match(s.opts.Exclude)
}

func (s *Scraper) warnOnce(source, family, reason string, args ...any) {
	if _, dup := s.warned.LoadOrStore(source+"\x00"+family+"\x00"+reason, struct{}{}); dup {
		return
	}
	s.opts.Logger.Warn("scrape: dropping metrics", append([]any{"source", source, "family", family, "reason", reason}, args...)...)
}

func (s *Scraper) scrapeOne(ctx context.Context, name, sockPath string) (src Source) {
	start := time.Now()
	src = Source{Name: name, Path: sockPath}
	defer func() { src.Duration = time.Since(start) }()

	ctx, cancel := context.WithTimeout(ctx, s.opts.Timeout)
	defer cancel()

	var d net.Dialer
	nc, err := d.DialContext(ctx, "unix", sockPath)
	if err != nil {
		src.Err = fmt.Errorf("dial: %w", err)
		return src
	}
	uc := nc.(*net.UnixConn)
	if peer, err := peerOf(uc); err != nil {
		src.UnitErr = err
	} else {
		src.Peer = &peer
		src.Unit, src.UnitErr = resolveUnit(s.opts.ProcRoot, peer.PID, name)
	}
	conn := varlink.NewConn(uc, varlink.WithMaxFrameSize(s.opts.MaxFrameSize), varlink.WithMaxFiles(0))
	defer conn.Close()
	client := metrics.New(conn)

	// Describe first, for types.
	index := map[string]int{}
	for d, err := range client.Describe(ctx) {
		if errors.Is(err, metrics.ErrNoSuchMetric) {
			break // systemd's answer for "no families"
		}
		if err != nil {
			src.Err = fmt.Errorf("io.systemd.Metrics.Describe: %w", err)
			return src
		}
		if !names.BelongsTo(d.Name, name) {
			s.warnOnce(name, d.Name, "family not named after its source")
			continue
		}
		t := Type(d.Type)
		if t != TypeCounter && t != TypeGauge && t != TypeString {
			s.warnOnce(name, d.Name, "unknown type", "type", string(d.Type))
			continue
		}
		if _, dup := index[d.Name]; dup {
			continue
		}
		index[d.Name] = len(src.Families)
		src.Families = append(src.Families, Family{Name: d.Name, Description: d.Description, Type: t})
	}

	kept := 0
	for m, err := range client.List(ctx) {
		if errors.Is(err, metrics.ErrNoSuchMetric) {
			break
		}
		if err != nil {
			src.Err = fmt.Errorf("io.systemd.Metrics.List: %w", err)
			return src
		}
		if !names.BelongsTo(m.Name, name) {
			src.Invalid++
			s.warnOnce(name, m.Name, "family not named after its source")
			continue
		}
		i, ok := index[m.Name]
		if !ok {
			src.Untyped++
			s.warnOnce(name, m.Name, "family has no known type")
			continue
		}
		if kept >= s.opts.MaxMetrics {
			src.Truncated++
			continue
		}
		f := &src.Families[i]
		v, err := decodeValue(m.Value)
		if err == nil && (f.Type == TypeString) != (v.Kind == KindString) {
			err = fmt.Errorf("%s value for %s family", v, f.Type)
		}
		if err != nil {
			src.Invalid++
			s.warnOnce(name, m.Name, "bad value", "err", err)
			continue
		}
		fields, err := decodeFields(m.Fields)
		if err != nil {
			src.Invalid++
			s.warnOnce(name, m.Name, "bad fields", "err", err)
			continue
		}
		f.Metrics = append(f.Metrics, Metric{Object: varlink.Deref(m.Object), Fields: fields, Value: v})
		kept++
	}
	if src.Truncated > 0 {
		s.warnOnce(name, "", "too many values", "limit", s.opts.MaxMetrics, "dropped", src.Truncated)
	}
	return src
}
