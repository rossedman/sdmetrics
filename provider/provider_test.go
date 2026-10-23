package provider_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rossedman/sdvarlink/systemd/v260/metrics"
	"github.com/rossedman/sdvarlink/varlink"

	"github.com/rossedman/sdmetrics/provider"
)

// serve starts p and returns a client for it.
func serve(t *testing.T, p *provider.Provider) *metrics.Client {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Serve: %v", err)
		}
	})
	return waitClient(t, p)
}

// waitClient dials p once its socket is up.
func waitClient(tb testing.TB, p *provider.Provider) *metrics.Client {
	tb.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		c, err := metrics.DialPath(context.Background(), p.Path())
		if err == nil {
			tb.Cleanup(func() { c.Close() })
			return c
		}
		if time.Now().After(deadline) {
			tb.Fatalf("dial %s: %v", p.Path(), err)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func newProvider(t *testing.T, name string, opts ...provider.Option) *provider.Provider {
	t.Helper()
	p, err := provider.New(name, append([]provider.Option{provider.WithDir(t.TempDir())}, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// list returns List replies as "name{object}{fields} value" lines, sorted.
func list(t *testing.T, c *metrics.Client) []string {
	t.Helper()
	var out []string
	for m, err := range c.List(context.Background()) {
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		line := m.Name
		if m.Object != nil {
			line += "{" + *m.Object + "}"
		}
		if len(m.Fields) > 0 {
			line += string(m.Fields)
		}
		out = append(out, line+" "+string(m.Value))
	}
	sort.Strings(out)
	return out
}

func describe(t *testing.T, c *metrics.Client) map[string]metrics.DescribeOut {
	t.Helper()
	out := map[string]metrics.DescribeOut{}
	for d, err := range c.Describe(context.Background()) {
		if err != nil {
			t.Fatalf("Describe: %v", err)
		}
		out[d.Name] = *d
	}
	return out
}

func equal(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

func TestQuickstart(t *testing.T) {
	p := newProvider(t, "io.example.Queue")
	depth := p.Gauge("Depth", provider.Help("Jobs waiting"))
	jobs := p.Counter("JobsProcessed", provider.Labels("result"))
	c := serve(t, p)

	depth.Set(42)
	jobs.With("ok").Inc()
	jobs.With("ok").Add(2)
	jobs.With("failed").Inc()

	equal(t, list(t, c), []string{
		`io.example.Queue.Depth 42`,
		`io.example.Queue.JobsProcessed{"result":"failed"} 1`,
		`io.example.Queue.JobsProcessed{"result":"ok"} 3`,
	})
	d := describe(t, c)
	if got := d["io.example.Queue.Depth"]; got.Type != metrics.MetricFamilyTypeGauge || got.Description != "Jobs waiting" {
		t.Errorf("Describe Depth = %+v", got)
	}
	if got := d["io.example.Queue.JobsProcessed"]; got.Type != metrics.MetricFamilyTypeCounter {
		t.Errorf("Describe JobsProcessed = %+v", got)
	}
}

func TestSocketFile(t *testing.T) {
	dir := t.TempDir()
	p, err := provider.New("io.example.Mode", provider.WithDir(dir), provider.WithSocketMode(0o640))
	if err != nil {
		t.Fatal(err)
	}
	p.Gauge("Up").Set(1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.Serve(ctx) }()
	path := filepath.Join(dir, "io.example.Mode")
	var fi os.FileInfo
	for i := 0; i < 500; i++ {
		if fi, err = os.Stat(path); err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSocket == 0 || fi.Mode().Perm() != 0o640 {
		t.Errorf("socket mode = %v", fi.Mode())
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("socket not removed after Serve: %v", err)
	}
	if err := p.Serve(context.Background()); err == nil {
		t.Error("second Serve succeeded")
	}
}

func TestEmptyProviderAnswersNoSuchMetric(t *testing.T) {
	p := newProvider(t, "io.example.Empty")
	p.Counter("Labelled", provider.Labels("x")) // no series yet
	c := serve(t, p)
	for _, err := range c.List(context.Background()) {
		if !errors.Is(err, metrics.ErrNoSuchMetric) {
			t.Fatalf("List on empty provider: %v", err)
		}
	}
}

func TestObjectsLabelsAndDelete(t *testing.T) {
	p := newProvider(t, "io.example.Net")
	state := p.String("OperState", provider.Help("Per interface state"))
	bytes := p.Counter("RxBytes", provider.Labels("queue"))
	c := serve(t, p)

	state.On("eth0").Set("routable")
	state.On("wlan0").Set("dormant")
	bytes.On("eth0").With("0").Add(100)
	bytes.With("1").On("eth0").Add(5)
	if got := bytes.On("eth0").With("0").Value(); got != 100 {
		t.Errorf("Value = %d", got)
	}
	equal(t, list(t, c), []string{
		`io.example.Net.OperState{eth0} "routable"`,
		`io.example.Net.OperState{wlan0} "dormant"`,
		`io.example.Net.RxBytes{eth0}{"queue":"0"} 100`,
		`io.example.Net.RxBytes{eth0}{"queue":"1"} 5`,
	})

	state.On("wlan0").Delete()
	bytes.On("eth0").With("1").Delete()
	equal(t, list(t, c), []string{
		`io.example.Net.OperState{eth0} "routable"`,
		`io.example.Net.RxBytes{eth0}{"queue":"0"} 100`,
	})
	// A deleted series comes back fresh.
	if got := bytes.On("eth0").With("1").Value(); got != 0 {
		t.Errorf("recreated series = %d", got)
	}
}

func TestGaugeArithmetic(t *testing.T) {
	p := newProvider(t, "io.example.G")
	g := p.Gauge("G")
	g.Set(1.5)
	g.Add(2)
	g.Inc()
	g.Dec()
	g.Add(-0.25)
	if got := g.Value(); got != 3.25 {
		t.Fatalf("Value = %v", got)
	}
	nan := p.Gauge("NaN")
	nan.Set(math.NaN())
	c := serve(t, p)
	equal(t, list(t, c), []string{`io.example.G.G 3.25`}) // NaN has no JSON form
}

func TestFuncMetrics(t *testing.T) {
	p := newProvider(t, "io.example.F")
	n := 0
	var mu sync.Mutex
	p.GaugeFunc("CacheSize", func() float64 { mu.Lock(); defer mu.Unlock(); n++; return float64(n) })
	p.CounterFunc("Uptime", func() uint64 { return 7 })
	p.StringFunc("Version", func() string { return "1.2.3" })
	p.StringFunc("Unset", func() string { return "" })
	c := serve(t, p)
	equal(t, list(t, c), []string{
		`io.example.F.CacheSize 1`,
		`io.example.F.Uptime 7`,
		`io.example.F.Version "1.2.3"`,
	})
	equal(t, list(t, c)[:1], []string{`io.example.F.CacheSize 2`})
	if got := len(describe(t, c)); got != 4 {
		t.Errorf("Describe families = %d, want 4", got)
	}
}

type staticCollector []provider.Family

func (s staticCollector) Collect(context.Context) ([]provider.Family, error) { return s, nil }

func TestCollector(t *testing.T) {
	p := newProvider(t, "io.example.C")
	p.Gauge("Taken").Set(1)
	p.Register(staticCollector{
		{Name: "Requests", Type: provider.TypeCounter, Description: "Requests served", Samples: []provider.Sample{
			{Fields: map[string]string{"code": "200"}, Value: uint64(10)},
			{Fields: map[string]string{"code": "500"}, Value: 2},
		}},
		{Name: "Temp", Type: provider.TypeGauge, Samples: []provider.Sample{{Object: "cpu0", Value: float32(41.5)}}},
		{Name: "Mode", Type: provider.TypeString, Samples: []provider.Sample{{Value: "active"}, {Value: 3}}}, // 3 is not a string
		{Name: "bad__name", Type: provider.TypeGauge, Samples: []provider.Sample{{Value: 1}}},
		{Name: "Taken", Type: provider.TypeGauge, Samples: []provider.Sample{{Value: 2}}},
		{Name: "Weird", Type: "histogram", Samples: []provider.Sample{{Value: 2}}},
	})
	c := serve(t, p)
	equal(t, list(t, c), []string{
		`io.example.C.Mode "active"`,
		`io.example.C.Requests{"code":"200"} 10`,
		`io.example.C.Requests{"code":"500"} 2`,
		`io.example.C.Taken 1`,
		`io.example.C.Temp{cpu0} 41.5`,
	})
	d := describe(t, c)
	if len(d) != 4 || d["io.example.C.Requests"].Description != "Requests served" {
		t.Errorf("Describe = %v", d)
	}
}

func TestRegistrationPanics(t *testing.T) {
	p := newProvider(t, "io.example.P")
	p.Gauge("Dup")
	for name, fn := range map[string]func(){
		"duplicate":           func() { p.Counter("Dup") },
		"invalid name":        func() { p.Gauge("bad name") },
		"double underscore":   func() { p.Gauge("a__b") },
		"invalid label":       func() { p.Gauge("L", provider.Labels("bad-label")) },
		"duplicate label":     func() { p.Gauge("L2", provider.Labels("a", "a")) },
		"labels on func":      func() { p.GaugeFunc("F", func() float64 { return 0 }, provider.Labels("a")) },
		"wrong label count":   func() { p.Counter("W", provider.Labels("a", "b")).With("x") },
		"labels on unlabeled": func() { p.Counter("U").With("x") },
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("no panic")
				}
			}()
			fn()
		})
	}
}

func TestInvalidProviderName(t *testing.T) {
	for _, name := range []string{"", "queue_daemon", "io..x", "1io.x", "io.x/y"} {
		if _, err := provider.New(name); err == nil {
			t.Errorf("New(%q) succeeded", name)
		}
	}
}

func TestIntrospection(t *testing.T) {
	p := newProvider(t, "io.example.I", provider.WithServerInfo(varlink.ServerInfo{Vendor: "Example", Product: "queued", Version: "1.0"}))
	p.Gauge("Up").Set(1)
	c := serve(t, p)
	info, err := varlink.GetInfo(context.Background(), c.Caller())
	if err != nil {
		t.Fatal(err)
	}
	if info.Vendor != "Example" || info.Product != "queued" || !strings.Contains(strings.Join(info.Interfaces, ","), metrics.InterfaceName) {
		t.Errorf("GetInfo = %+v", info)
	}
	desc, err := varlink.GetInterfaceDescription(context.Background(), c.Caller(), metrics.InterfaceName)
	if err != nil || desc != metrics.InterfaceDescription {
		t.Errorf("GetInterfaceDescription: %v", err)
	}
}

func TestRegisterWhileServing(t *testing.T) {
	p := newProvider(t, "io.example.Live")
	p.Gauge("First").Set(1)
	c := serve(t, p)
	p.Gauge("Second").Set(2)
	equal(t, list(t, c), []string{`io.example.Live.First 1`, `io.example.Live.Second 2`})
}

func TestConcurrentUpdatesAndReads(t *testing.T) {
	p := newProvider(t, "io.example.Race")
	g := p.Gauge("G", provider.Labels("worker"))
	ctr := p.Counter("C", provider.Labels("worker"))
	// Publish one series up front: reads may start before any worker has
	// written, and a provider with nothing to publish answers NoSuchMetric.
	g.With("0").Set(0)
	c := serve(t, p)

	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			label := fmt.Sprint(w % 4)
			for i := 0; i < 2000; i++ {
				g.With(label).Add(1)
				ctr.With(label).Inc()
				if i%500 == 0 {
					ctr.With(fmt.Sprintf("tmp-%d-%d", w, i)).Inc()
				}
			}
		}()
	}
	for i := 0; i < 20; i++ {
		_ = list(t, c)
	}
	wg.Wait()
	var total uint64
	for w := 0; w < 4; w++ {
		total += ctr.With(fmt.Sprint(w)).Value()
	}
	if total != 16000 {
		t.Errorf("counter total = %d, want 16000", total)
	}
	for _, line := range list(t, c) {
		var v json.Number
		if err := json.Unmarshal([]byte(line[strings.LastIndexByte(line, ' ')+1:]), &v); err != nil {
			t.Errorf("bad value in %q", line)
		}
	}
}

func TestUpdatesDoNotAllocate(t *testing.T) {
	p := newProvider(t, "io.example.Alloc")
	g := p.Gauge("G", provider.Labels("a", "b"))
	ctr := p.Counter("C", provider.Labels("a"))
	plain := p.Counter("P")
	g.With("x", "y").Set(1)
	ctr.With("x").Inc()
	if n := testing.AllocsPerRun(1000, func() {
		g.With("x", "y").Add(1)
		ctr.With("x").Inc()
		ctr.On("obj").With("x").Inc()
		plain.Inc()
	}); n > 0 {
		t.Errorf("updates allocate %v times per run", n)
	}
}
