package exporter

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	colmetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/protobuf/proto"

	"github.com/rossedman/sdmetrics/otelmap"
)

func TestOTLPHTTPEndToEnd(t *testing.T) {
	dir := startProvider(t)
	var mu sync.Mutex
	var sources []string
	got := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/metrics" || r.Header.Get("X-Token") != "secret" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		b, _ := io.ReadAll(r.Body)
		var req colmetrics.ExportMetricsServiceRequest
		if err := proto.Unmarshal(b, &req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		for _, rm := range req.ResourceMetrics {
			for _, kv := range rm.Resource.Attributes {
				if kv.Key == "service.name" {
					sources = append(sources, kv.Value.GetStringValue())
				}
			}
		}
		mu.Unlock()
		select {
		case got <- struct{}{}:
		default:
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
		out, _ := proto.Marshal(&colmetrics.ExportMetricsServiceResponse{})
		w.Write(out)
	}))
	defer srv.Close()

	cfg := testConfig(dir)
	cfg.OTLP.Protocol = "http/protobuf"
	cfg.OTLP.Endpoint = srv.URL + "/v1/metrics" // a URL: its scheme decides TLS
	cfg.OTLP.Headers = map[string]string{"X-Token": "secret"}
	cfg.OTLP.Compression = "none"
	runExporter(t, cfg)
	select {
	case <-got:
	case <-time.After(10 * time.Second):
		t.Fatal("no OTLP/HTTP export arrived")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n := len(sources)
		mu.Unlock()
		if n >= 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(strings.Join(sources, ","), "queue") {
		t.Errorf("services exported: %v", sources)
	}
}

func TestPusherOptions(t *testing.T) {
	ctx := context.Background()
	for _, c := range []OTLPConfig{
		{Protocol: "grpc", Endpoint: "localhost:4317", Insecure: true, Headers: map[string]string{"a": "b"}, Compression: "gzip", Timeout: time.Second},
		{Protocol: "grpc", Endpoint: "https://otel.example:4317", Timeout: time.Second},
		{Protocol: "grpc", Timeout: time.Second},
		{Protocol: "http/protobuf", Endpoint: "localhost:4318", Insecure: true, Headers: map[string]string{"a": "b"}, Compression: "gzip", Timeout: time.Second},
		{Protocol: "http", Endpoint: "https://otel.example/v1/metrics", Timeout: time.Second},
		{Protocol: "http/protobuf", Timeout: time.Second},
	} {
		p, err := newPusher(ctx, c)
		if err != nil {
			t.Errorf("%+v: %v", c, err)
			continue
		}
		p.Shutdown(ctx)
	}
}

// fakePusher fails the first exports, then succeeds.
type fakePusher struct {
	failures atomic.Int32
	exports  atomic.Int32
}

func (f *fakePusher) Export(context.Context, *metricdata.ResourceMetrics) error {
	f.exports.Add(1)
	if f.failures.Add(-1) >= 0 {
		return errors.New("collector unavailable")
	}
	return nil
}
func (f *fakePusher) Shutdown(context.Context) error { return errors.New("already closed") }

func TestExportFailureAndRecovery(t *testing.T) {
	dir := startProvider(t)
	cfg := Default()
	cfg.Dir = dir
	cfg.Interval = 20 * time.Millisecond
	cfg.Timeout = 10 * time.Millisecond
	cfg.Prometheus.Listen = "127.0.0.1:0"
	ctx, cancel := context.WithCancel(context.Background())
	e, err := New(ctx, cfg, quiet, "test")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakePusher{}
	f.failures.Store(4) // two scrapes of two resources
	e.push = f
	done := make(chan error, 1)
	go func() { done <- e.Run(ctx) }()
	deadline := time.Now().Add(10 * time.Second)
	for f.exports.Load() < 8 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if f.exports.Load() < 8 {
		t.Fatalf("exports = %d", f.exports.Load())
	}
	err = e.once(context.Background())
	if err != nil {
		t.Errorf("once after recovery: %v", err)
	}
	f.failures.Store(100)
	if err := e.once(context.Background()); err == nil || !strings.Contains(err.Error(), "resources failed") {
		t.Errorf("once with failing pusher: %v", err)
	}
}

func TestPrometheusPortInUse(t *testing.T) {
	dir := startProvider(t)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	cfg := Default()
	cfg.Dir = dir
	cfg.Prometheus.Listen = l.Addr().String()
	e, err := New(context.Background(), cfg, quiet, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "prometheus endpoint") {
		t.Fatalf("Run on a busy port: %v", err)
	}
}

func TestOnceScrapeError(t *testing.T) {
	cfg := Default()
	cfg.Dir = t.TempDir() + "/missing"
	cfg.Prometheus.Listen = "127.0.0.1:0"
	e, _ := New(context.Background(), cfg, quiet, "test")
	if err := e.once(context.Background()); err == nil {
		t.Fatal("once without a report directory succeeded")
	}
}

func TestPromResourceLabelsAndPages(t *testing.T) {
	dir := startProvider(t)
	cfg := testConfig(dir)
	cfg.Prometheus.Listen = "127.0.0.1:0"
	cfg.Resource = map[string]string{"deployment.environment": "test"}
	e := runExporter(t, cfg)
	body, addr := scrapePromAddr(t, e)
	if !strings.Contains(body, `deployment_environment="test"`) {
		t.Error("configured resource attribute not copied to samples")
	}
	for path, want := range map[string]int{"/": http.StatusOK, "/nope": http.StatusNotFound, "/healthz": http.StatusOK} {
		resp, err := http.Get("http://" + addr + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("GET %s = %d, want %d", path, resp.StatusCode, want)
		}
	}
	ch := make(chan *prometheus.Desc, 1)
	e.prom.Describe(ch)
	if len(ch) != 0 {
		t.Error("Describe sent descriptors for an unchecked collector")
	}
}

func TestHealthzBeforeFirstScrape(t *testing.T) {
	e := &Exporter{ready: make(chan struct{}), prom: newPromCollector(0, nil), cfg: Default(), log: quiet}
	e.cfg.Prometheus.Listen = "127.0.0.1:0"
	srv, l, err := e.promServer()
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(l)
	defer srv.Close()
	resp, err := http.Get("http://" + l.Addr().String() + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("healthz before a scrape = %d", resp.StatusCode)
	}
	resp, _ = http.Get("http://" + l.Addr().String() + "/metrics")
	resp.Body.Close() // no batch yet: only runtime metrics, no error
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/metrics before a scrape = %d", resp.StatusCode)
	}
}

func TestPromNamesInOTelMode(t *testing.T) {
	c := newPromCollector(otelmap.NamingOTel, nil)
	for _, tc := range []struct {
		m    otelmap.Metric
		want string
	}{
		{otelmap.Metric{Name: "io.example.api.latency", Unit: "s", Kind: otelmap.Gauge}, "io_example_api_latency_seconds"},
		{otelmap.Metric{Name: "io.example.api.requests", Kind: otelmap.Sum, Monotonic: true}, "io_example_api_requests_total"},
		{otelmap.Metric{Name: "io.example.api.d_bucket", Kind: otelmap.Sum, Monotonic: true}, "io_example_api_d_bucket"},
		{otelmap.Metric{Name: "io.example.api.d_count", Kind: otelmap.Sum, Monotonic: true}, "io_example_api_d_count"},
		{otelmap.Metric{Name: "sdmetrics.source.up", Kind: otelmap.Gauge}, "sdmetrics_source_up"},
		{otelmap.Metric{Name: "io.example.cpu", Unit: "1", Kind: otelmap.Gauge}, "io_example_cpu_ratio"},
	} {
		if got := c.promName(tc.m); got != tc.want {
			t.Errorf("promName(%s) = %s, want %s", tc.m.Name, got, tc.want)
		}
	}
}
