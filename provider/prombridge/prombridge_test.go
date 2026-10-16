package prombridge_test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/rossedman/sdmetrics/provider"
	"github.com/rossedman/sdmetrics/provider/prombridge"
	"github.com/rossedman/sdmetrics/scrape"
)

// roundTrip serves c from a provider and scrapes it back as
// "family{fields} type value" lines.
func roundTrip(t *testing.T, c provider.Collector) []string {
	t.Helper()
	dir, err := os.MkdirTemp("", "report")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	p, err := provider.New("io.example.Api", provider.WithDir(dir), provider.WithLogger(quiet))
	if err != nil {
		t.Fatal(err)
	}
	p.Register(c)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { p.Serve(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	for i := 0; i < 1000; i++ {
		if c, err := net.Dial("unix", p.Path()); err == nil {
			c.Close()
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	res, err := scrape.New(scrape.Options{Dir: dir, Logger: quiet}).Scrape(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	src := res.Sources[0]
	if src.Err != nil || src.Invalid+src.Untyped > 0 {
		t.Fatalf("scrape: %+v", src)
	}
	var out []string
	for _, f := range src.Families {
		for _, m := range f.Metrics {
			var fs []string
			for k, v := range m.Fields {
				fs = append(fs, k+"="+v)
			}
			sort.Strings(fs)
			out = append(out, f.Name+"{"+strings.Join(fs, ",")+"} "+string(f.Type)+" "+m.Value.String())
		}
	}
	sort.Strings(out)
	return out
}

func TestRoundTrip(t *testing.T) {
	reg := prometheus.NewRegistry()
	reqs := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "http_requests_total", Help: "Requests"}, []string{"code"})
	reqs.WithLabelValues("200").Add(3)
	reqs.WithLabelValues("500").Add(0.5)
	inflight := prometheus.NewGauge(prometheus.GaugeOpts{Name: "inflight", Help: "In flight"})
	inflight.Set(2)
	lat := prometheus.NewHistogram(prometheus.HistogramOpts{Name: "latency_seconds", Help: "Latency", Buckets: []float64{0.1, 1}})
	lat.Observe(0.05)
	lat.Observe(0.5)
	lat.Observe(5)
	sz := prometheus.NewSummary(prometheus.SummaryOpts{Name: "size_bytes", Help: "Size", Objectives: map[float64]float64{0.5: 0.05}})
	sz.Observe(10)
	odd := prometheus.NewGauge(prometheus.GaugeOpts{Name: "go:odd__name", Help: "Odd"})
	odd.Set(1)
	reg.MustRegister(reqs, inflight, lat, sz, odd)

	got := roundTrip(t, prombridge.New(reg))
	want := []string{
		`io.example.Api.go_odd_name{} gauge 1`,
		`io.example.Api.http_requests_total{code=200} counter 3`,
		`io.example.Api.http_requests_total{code=500} counter 0.5`,
		`io.example.Api.inflight{} gauge 2`,
		`io.example.Api.latency_seconds_bucket{le=+Inf} counter 3`,
		`io.example.Api.latency_seconds_bucket{le=0.1} counter 1`,
		`io.example.Api.latency_seconds_bucket{le=1} counter 2`,
		`io.example.Api.latency_seconds_count{} counter 3`,
		`io.example.Api.latency_seconds_sum{} counter 5.55`,
		`io.example.Api.size_bytes_count{} counter 1`,
		`io.example.Api.size_bytes_sum{} counter 10`,
		`io.example.Api.size_bytes{quantile=0.5} gauge 10`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

func TestDefaultGatherer(t *testing.T) {
	// The default registry's Go and process collectors convert cleanly.
	got := roundTrip(t, prombridge.New(prometheus.DefaultGatherer))
	found := false
	for _, l := range got {
		if strings.HasPrefix(l, "io.example.Api.go_goroutines{} gauge ") {
			found = true
		}
	}
	if !found {
		t.Errorf("go_goroutines missing from %d values", len(got))
	}
}

func TestUntypedAndImplicitInfBucket(t *testing.T) {
	reg := prometheus.NewRegistry()
	reg.MustRegister(prometheus.NewUntypedFunc(prometheus.UntypedOpts{Name: "legacy", Help: "Untyped"}, func() float64 { return 4 }))
	// A histogram without an explicit +Inf bucket gets one.
	h := prometheus.MustNewConstHistogram(prometheus.NewDesc("noinf", "No +Inf", nil, nil), 3, 6, map[float64]uint64{1: 1, 2: 2})
	reg.MustRegister(constCollector{h})
	got := strings.Join(roundTrip(t, prombridge.New(reg)), "\n")
	for _, want := range []string{
		"io.example.Api.legacy{} gauge 4",
		"io.example.Api.noinf_bucket{le=+Inf} counter 3",
		"io.example.Api.noinf_bucket{le=2} counter 2",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
}

type constCollector struct{ m prometheus.Metric }

func (c constCollector) Describe(ch chan<- *prometheus.Desc) { ch <- c.m.Desc() }
func (c constCollector) Collect(ch chan<- prometheus.Metric) { ch <- c.m }
