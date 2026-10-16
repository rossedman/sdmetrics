package scrape_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/rossedman/sdvarlink/systemd/v260/metrics"
	"github.com/rossedman/sdvarlink/varlink"
	"github.com/rossedman/sdvarlink/varlinktest"

	"github.com/rossedman/sdmetrics/provider"
	"github.com/rossedman/sdmetrics/scrape"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// reportDir returns a short temporary directory (socket paths are limited
// to 108 bytes) and a fake /proc that puts this process in unit.
func reportDir(t *testing.T, unit string) (dir, proc string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "report")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	proc = t.TempDir()
	pidDir := filepath.Join(proc, strconv.Itoa(os.Getpid()))
	if err := os.MkdirAll(pidDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cg := "0::/system.slice/" + unit + "\n"
	if err := os.WriteFile(filepath.Join(pidDir, "cgroup"), []byte(cg), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, proc
}

// startProvider serves p and waits for its socket.
func startProvider(t *testing.T, p *provider.Provider) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.Serve(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	for i := 0; ; i++ {
		if c, err := net.Dial("unix", p.Path()); err == nil {
			c.Close()
			return
		}
		if i > 1000 {
			t.Fatalf("provider %s did not start", p.Name())
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// fake starts a varlinktest server answering io.systemd.Metrics and links
// it into dir as name.
func fake(t *testing.T, dir, name string, describe []metrics.DescribeOut, list []metrics.ListOut, delay time.Duration, failWith error) {
	t.Helper()
	srv := varlinktest.NewServer(t)
	srv.AddInterface(metrics.InterfaceDescription)
	stream := func(n int, get func(int) any) iter.Seq2[any, error] {
		return func(yield func(any, error) bool) {
			if delay > 0 {
				time.Sleep(delay)
			}
			if failWith != nil {
				yield(nil, failWith)
				return
			}
			if n == 0 {
				yield(nil, &metrics.NoSuchMetricError{})
				return
			}
			for i := 0; i < n; i++ {
				if !yield(get(i), nil) {
					return
				}
			}
		}
	}
	varlinktest.OnStream(srv, "io.systemd.Metrics.Describe", func(context.Context, struct{}) iter.Seq2[any, error] {
		return stream(len(describe), func(i int) any { return describe[i] })
	})
	varlinktest.OnStream(srv, "io.systemd.Metrics.List", func(context.Context, struct{}) iter.Seq2[any, error] {
		return stream(len(list), func(i int) any { return list[i] })
	})
	if err := os.Symlink(srv.Path(), filepath.Join(dir, name)); err != nil {
		t.Fatal(err)
	}
}

func lo(name, object string, fields map[string]string, value string) metrics.ListOut {
	out := metrics.ListOut{Name: name, Value: json.RawMessage(value)}
	if object != "" {
		out.Object = varlink.Ptr(object)
	}
	if fields != nil {
		out.Fields, _ = json.Marshal(fields)
	}
	return out
}

func bySource(res *scrape.Result) map[string]scrape.Source {
	m := map[string]scrape.Source{}
	for _, s := range res.Sources {
		m[s.Name] = s
	}
	return m
}

func TestScrape(t *testing.T) {
	dir, proc := reportDir(t, "queue.service")

	// A healthy source built with the provider SDK, bound in place.
	p, err := provider.New("io.example.Queue", provider.WithDir(dir), provider.WithLogger(quiet))
	if err != nil {
		t.Fatal(err)
	}
	p.Gauge("Depth", provider.Help("Jobs waiting")).Set(3)
	p.Counter("Jobs", provider.Labels("result")).With("ok").Add(9)
	p.String("State").On("worker-1").Set("busy")
	startProvider(t, p)

	// A source answering like systemd, through a symlink.
	fake(t, dir, "io.systemd.Network",
		[]metrics.DescribeOut{
			{Name: "io.systemd.Network.ManagedInterfaces", Type: metrics.MetricFamilyTypeGauge},
			{Name: "io.systemd.Network.OperationalState", Type: metrics.MetricFamilyTypeString},
			{Name: "io.systemd.Network.Future", Type: "histogram"},
			{Name: "io.systemd.Other.Wrong", Type: metrics.MetricFamilyTypeGauge},
		},
		[]metrics.ListOut{
			lo("io.systemd.Network.ManagedInterfaces", "", nil, "2"),
			lo("io.systemd.Network.OperationalState", "eth0", nil, `"routable"`),
			lo("io.systemd.Network.OperationalState", "eth1", nil, `7`),                 // wrong kind
			lo("io.systemd.Network.Future", "", nil, "1"),                               // untyped
			lo("io.systemd.Network.Undescribed", "", nil, "1"),                          // untyped
			lo("io.systemd.Other.Wrong", "", nil, "1"),                                  // wrong prefix
			lo("io.systemd.Network.ManagedInterfaces", "", nil, "1.5e300"),              // float
			lo("io.systemd.Network.ManagedInterfaces", "", nil, `{"x":1}`),              // not a number
			lo("io.systemd.Network.ManagedInterfaces", "", nil, "18446744073709551615"), // > int64
		}, 0, nil)

	// An empty source, a failing one and a slow one.
	fake(t, dir, "io.example.Empty", nil, nil, 0, nil)
	fake(t, dir, "io.example.Broken", nil, nil, 0, &varlink.Error{Name: "io.example.Broken.Oops"})
	fake(t, dir, "io.example.Slow", nil, nil, 2*time.Second, nil)

	// Entries that are not sources.
	os.WriteFile(filepath.Join(dir, "io.example.File"), nil, 0o644)
	os.WriteFile(filepath.Join(dir, ".io.example.Queue.lock"), nil, 0o600)
	os.Mkdir(filepath.Join(dir, "io.example.Dir"), 0o755)
	os.Symlink("/nonexistent", filepath.Join(dir, "io.example.Dangling"))
	stale, _ := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(dir, "io.example.Stale"), Net: "unix"})
	stale.SetUnlinkOnClose(false)
	stale.Close()

	s := scrape.New(scrape.Options{Dir: dir, ProcRoot: proc, Timeout: 300 * time.Millisecond, Logger: quiet})
	start := time.Now()
	res, err := s.Scrape(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 1500*time.Millisecond {
		t.Errorf("scrape took %v; the slow source should be cut off at its timeout", took)
	}
	var got []string
	for _, src := range res.Sources {
		got = append(got, src.Name)
	}
	want := []string{"io.example.Broken", "io.example.Empty", "io.example.Queue", "io.example.Slow", "io.example.Stale", "io.systemd.Network"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("sources = %v, want %v", got, want)
	}
	srcs := bySource(res)

	q := srcs["io.example.Queue"]
	if q.Err != nil || q.Unit != "queue.service" || q.Peer == nil || q.Peer.PID != int32(os.Getpid()) {
		t.Fatalf("Queue = %+v", q)
	}
	if len(q.Families) != 3 {
		t.Fatalf("Queue families = %+v", q.Families)
	}
	if f := q.Families[0]; f.Name != "io.example.Queue.Depth" || f.Type != scrape.TypeGauge || f.Description != "Jobs waiting" || f.Metrics[0].Value.Int != 3 {
		t.Errorf("Depth = %+v", f)
	}
	if f := q.Families[1]; f.Type != scrape.TypeCounter || f.Metrics[0].Fields["result"] != "ok" || f.Metrics[0].Value.Number() != 9 {
		t.Errorf("Jobs = %+v", f)
	}
	if f := q.Families[2]; f.Type != scrape.TypeString || f.Metrics[0].Object != "worker-1" || f.Metrics[0].Value.Str != "busy" {
		t.Errorf("State = %+v", f)
	}

	n := srcs["io.systemd.Network"]
	if n.Err != nil {
		t.Fatal(n.Err)
	}
	if len(n.Families) != 2 || n.Invalid != 3 || n.Untyped != 2 {
		t.Errorf("Network: families=%d invalid=%d untyped=%d", len(n.Families), n.Invalid, n.Untyped)
	}
	mi := n.Families[0].Metrics
	if len(mi) != 3 || mi[0].Value.Kind != scrape.KindInt || mi[1].Value.Kind != scrape.KindFloat || mi[2].Value.Float != 18446744073709551615 {
		t.Errorf("ManagedInterfaces = %+v", mi)
	}

	if e := srcs["io.example.Empty"]; e.Err != nil || len(e.Families) != 0 {
		t.Errorf("Empty = %+v", e)
	}
	if b := srcs["io.example.Broken"]; b.Err == nil || varlink.ErrorName(errors.Unwrap(b.Err)) != "io.example.Broken.Oops" {
		t.Errorf("Broken.Err = %v", b.Err)
	}
	if sl := srcs["io.example.Slow"]; !errors.Is(sl.Err, context.DeadlineExceeded) {
		t.Errorf("Slow.Err = %v", sl.Err)
	}
	if st := srcs["io.example.Stale"]; st.Err == nil {
		t.Error("Stale source scraped without error")
	}
}

func TestIncludeExclude(t *testing.T) {
	dir, proc := reportDir(t, "x.service")
	for _, name := range []string{"io.systemd.Manager", "io.systemd.Network", "io.example.A", "io.example.B"} {
		fake(t, dir, name, nil, nil, 0, nil)
	}
	for _, tc := range []struct {
		include, exclude []string
		want             string
	}{
		{nil, nil, "[io.example.A io.example.B io.systemd.Manager io.systemd.Network]"},
		{[]string{"io.systemd.*"}, nil, "[io.systemd.Manager io.systemd.Network]"},
		{[]string{"io.systemd.*", "io.example.A"}, []string{"io.systemd.Network"}, "[io.example.A io.systemd.Manager]"},
		{nil, []string{"io.example.*"}, "[io.systemd.Manager io.systemd.Network]"},
	} {
		res, err := scrape.New(scrape.Options{Dir: dir, ProcRoot: proc, Include: tc.include, Exclude: tc.exclude, Logger: quiet}).Scrape(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, s := range res.Sources {
			got = append(got, s.Name)
		}
		if fmt.Sprint(got) != tc.want {
			t.Errorf("include=%v exclude=%v: %v, want %v", tc.include, tc.exclude, got, tc.want)
		}
	}
	if _, err := scrape.New(scrape.Options{Dir: dir, Include: []string{"["}}).Scrape(context.Background()); err == nil {
		t.Error("bad pattern accepted")
	}
}

func TestNoReportDir(t *testing.T) {
	_, err := scrape.New(scrape.Options{Dir: filepath.Join(t.TempDir(), "missing")}).Scrape(context.Background())
	if !errors.Is(err, scrape.ErrNoReportDir) {
		t.Fatalf("err = %v", err)
	}
}

func TestUnitResolutionFallback(t *testing.T) {
	dir, _ := reportDir(t, "x.service")
	fake(t, dir, "io.example.A", nil, nil, 0, nil)
	// /proc without our PID, as under hidepid= or ProtectProc=.
	res, err := scrape.New(scrape.Options{Dir: dir, ProcRoot: t.TempDir(), Logger: quiet}).Scrape(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	src := res.Sources[0]
	if src.Err != nil || src.Unit != "" || src.UnitErr == nil || src.Peer == nil {
		t.Fatalf("source = %+v", src)
	}
}

func TestMaxMetrics(t *testing.T) {
	dir, proc := reportDir(t, "x.service")
	var list []metrics.ListOut
	for i := 0; i < 10; i++ {
		list = append(list, lo("io.example.Big.V", strconv.Itoa(i), nil, "1"))
	}
	fake(t, dir, "io.example.Big", []metrics.DescribeOut{{Name: "io.example.Big.V", Type: metrics.MetricFamilyTypeGauge}}, list, 0, nil)
	res, err := scrape.New(scrape.Options{Dir: dir, ProcRoot: proc, MaxMetrics: 4, Logger: quiet}).Scrape(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if src := res.Sources[0]; len(src.Families[0].Metrics) != 4 || src.Truncated != 6 {
		t.Fatalf("kept %d, truncated %d", len(src.Families[0].Metrics), src.Truncated)
	}
}

func TestFrameSizeLimit(t *testing.T) {
	dir, proc := reportDir(t, "x.service")
	huge := `"` + string(make([]byte, 0)) + fmt.Sprintf("%0*d", 4096, 0) + `"`
	fake(t, dir, "io.example.Huge", []metrics.DescribeOut{{Name: "io.example.Huge.S", Type: metrics.MetricFamilyTypeString}},
		[]metrics.ListOut{lo("io.example.Huge.S", "", nil, huge)}, 0, nil)
	res, err := scrape.New(scrape.Options{Dir: dir, ProcRoot: proc, MaxFrameSize: 1024, Logger: quiet}).Scrape(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Sources[0].Err == nil {
		t.Fatal("oversized reply accepted")
	}
}
