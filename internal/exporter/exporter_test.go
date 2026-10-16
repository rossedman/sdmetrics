package exporter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	colmetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/grpc"

	"github.com/rossedman/sdmetrics/provider"
	"github.com/rossedman/sdmetrics/scrape"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// sink is an in-memory OTLP gRPC metrics receiver.
type sink struct {
	colmetrics.UnimplementedMetricsServiceServer
	mu   sync.Mutex
	reqs []*colmetrics.ExportMetricsServiceRequest
	got  chan struct{}
}

func (s *sink) Export(_ context.Context, req *colmetrics.ExportMetricsServiceRequest) (*colmetrics.ExportMetricsServiceResponse, error) {
	s.mu.Lock()
	s.reqs = append(s.reqs, req)
	s.mu.Unlock()
	select {
	case s.got <- struct{}{}:
	default:
	}
	return &colmetrics.ExportMetricsServiceResponse{}, nil
}

func startSink(t *testing.T) (*sink, string) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &sink{got: make(chan struct{}, 1)}
	srv := grpc.NewServer()
	colmetrics.RegisterMetricsServiceServer(srv, s)
	go srv.Serve(l)
	t.Cleanup(srv.Stop)
	return s, l.Addr().String()
}

// startProvider runs a provider in a fresh report directory.
func startProvider(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "report")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	p, err := provider.New("io.example.Queue", provider.WithDir(dir), provider.WithLogger(quiet))
	if err != nil {
		t.Fatal(err)
	}
	p.Gauge("Depth", provider.Help("Jobs waiting")).Set(42)
	jobs := p.Counter("JobsProcessedTotal", provider.Labels("result"))
	jobs.With("ok").Add(7)
	p.String("State").On("worker-1").Set("busy")
	p.GaugeFunc("LatencySeconds", func() float64 { return 0.25 })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { p.Serve(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	for i := 0; i < 1000; i++ {
		if c, err := net.Dial("unix", p.Path()); err == nil {
			c.Close()
			return dir
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("provider did not start")
	return ""
}

// testConfig scrapes dir and pins the test provider's unit, so results do
// not depend on whether the test itself runs inside a systemd unit (as on
// CI runners) or not.
func testConfig(dir string) Config {
	cfg := Default()
	cfg.Dir = dir
	cfg.Interval = time.Hour
	cfg.Units = map[string]string{"io.example.Queue": "queue.service"}
	return cfg
}

func runExporter(t *testing.T, cfg Config) *Exporter {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	e, err := New(ctx, cfg, quiet, "test")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- e.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Run: %v", err)
		}
	})
	return e
}

func TestOTLPEndToEnd(t *testing.T) {
	dir := startProvider(t)
	s, addr := startSink(t)
	cfg := testConfig(dir)
	cfg.OTLP.Endpoint = addr
	cfg.OTLP.Insecure = true
	cfg.Resource = map[string]string{"deployment.environment": "test"}
	runExporter(t, cfg)

	select {
	case <-s.got:
	case <-time.After(10 * time.Second):
		t.Fatal("no OTLP export arrived")
	}
	// Exports are one request per resource; wait for the self-metrics too.
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.mu.Lock()
		n := len(s.reqs)
		s.mu.Unlock()
		if n >= 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	var queue *metricspb.ResourceMetrics
	sources := map[string]bool{}
	for _, req := range s.reqs {
		for _, rm := range req.ResourceMetrics {
			attrs := map[string]string{}
			for _, kv := range rm.Resource.Attributes {
				attrs[kv.Key] = kv.Value.GetStringValue()
			}
			if attrs["deployment.environment"] != "test" && attrs["service.name"] != "sdmetrics" {
				t.Errorf("resource without configured attributes: %v", attrs)
			}
			sources[attrs["service.name"]] = true
			if attrs["systemd.report.source"] == "io.example.Queue" {
				queue = rm
			}
		}
	}
	if queue == nil || !sources["sdmetrics"] {
		t.Fatalf("resources: %v", sources)
	}
	byName := map[string]*metricspb.Metric{}
	for _, m := range queue.ScopeMetrics[0].Metrics {
		byName[m.Name] = m
	}
	if g := byName["io.example.Queue.Depth"].GetGauge(); g == nil || g.DataPoints[0].GetAsInt() != 42 {
		t.Errorf("Depth = %v", byName["io.example.Queue.Depth"])
	}
	sum := byName["io.example.Queue.JobsProcessedTotal"].GetSum()
	if sum == nil || !sum.IsMonotonic || sum.AggregationTemporality != metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE || sum.DataPoints[0].GetAsInt() != 7 {
		t.Errorf("JobsProcessedTotal = %v", byName["io.example.Queue.JobsProcessedTotal"])
	}
	if sum != nil && sum.DataPoints[0].StartTimeUnixNano == 0 {
		t.Error("counter without start time")
	}
	if g := byName["io.example.Queue.State"].GetGauge(); g == nil || g.DataPoints[0].GetAsInt() != 1 {
		t.Errorf("State = %v", byName["io.example.Queue.State"])
	}
	if g := byName["io.example.Queue.LatencySeconds"].GetGauge(); g == nil || g.DataPoints[0].GetAsDouble() != 0.25 {
		t.Errorf("LatencySeconds = %v", byName["io.example.Queue.LatencySeconds"])
	}
}

func scrapeProm(t *testing.T, e *Exporter) string {
	t.Helper()
	body, _ := scrapePromAddr(t, e)
	return body
}

// scrapePromAddr waits for the first scrape and returns /metrics and the
// endpoint's address.
func scrapePromAddr(t *testing.T, e *Exporter) (string, string) {
	t.Helper()
	var addr string
	select {
	case addr = <-e.addr:
	case <-time.After(10 * time.Second):
		t.Fatal("Prometheus endpoint did not start")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err := http.Get("http://" + addr + "/healthz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("exporter never became ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	resp, err := http.Get("http://" + addr + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/metrics: %s\n%s", resp.Status, b)
	}
	return string(b), addr
}

func TestPrometheusRaw(t *testing.T) {
	dir := startProvider(t)
	cfg := testConfig(dir)
	cfg.Prometheus.Listen = "127.0.0.1:0"
	body := scrapeProm(t, runExporter(t, cfg))
	for _, want := range []string{
		"# HELP io_example_Queue_Depth Jobs waiting",
		"# TYPE io_example_Queue_Depth gauge",
		`io_example_Queue_Depth{service_name="queue",systemd_report_source="io.example.Queue",systemd_unit_name="queue.service"} 42`,
		"# TYPE io_example_Queue_JobsProcessedTotal counter",
		`io_example_Queue_JobsProcessedTotal{result="ok",service_name="queue",systemd_report_source="io.example.Queue",systemd_unit_name="queue.service"} 7`,
		`io_example_Queue_State{service_name="queue",systemd_object="worker-1",systemd_report_source="io.example.Queue",systemd_unit_name="queue.service",systemd_value="busy"} 1`,
		`sdmetrics_source_up{service_name="sdmetrics",systemd_report_source="io.example.Queue"} 1`,
		"go_goroutines",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics lacks %q", want)
		}
	}
	if t.Failed() {
		t.Log(body)
	}
}

func TestPrometheusOTelNaming(t *testing.T) {
	dir := startProvider(t)
	cfg := testConfig(dir)
	cfg.Naming = "otel"
	cfg.Prometheus.Listen = "127.0.0.1:0"
	body := scrapeProm(t, runExporter(t, cfg))
	for _, want := range []string{
		"io_example_queue_depth{",
		"# TYPE io_example_queue_jobs_processed_total counter",
		"io_example_queue_latency_seconds{",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics lacks %q", want)
		}
	}
	if t.Failed() {
		t.Log(body)
	}
}

func TestNoReportDirAtStartup(t *testing.T) {
	cfg := Default()
	cfg.Dir = filepath.Join(t.TempDir(), "missing")
	cfg.Prometheus.Listen = "127.0.0.1:0"
	e, err := New(context.Background(), cfg, quiet, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Run(context.Background()); !errors.Is(err, ErrNoReportDir) {
		t.Fatalf("Run = %v, want ErrNoReportDir", err)
	}
}

func TestNothingToExport(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "")
	if _, err := New(context.Background(), Default(), quiet, "test"); err == nil {
		t.Fatal("exporter with no outputs accepted")
	}
}

func TestConfig(t *testing.T) {
	file := filepath.Join(t.TempDir(), "c.yaml")
	os.WriteFile(file, []byte(`
interval: 15s
timeout: 1s
include: ["io.systemd.*", "io.example.*"]
naming: otel
otlp:
  endpoint: otel-gateway.internal:4317
  protocol: grpc
  headers: {authorization: "Bearer x"}
prometheus:
  listen: ":9558"
resource:
  deployment.environment: prod
units:
  io.example.Queue: queue.service
manager_socket: /run/user/1000/systemd/io.systemd.Manager
`), 0o644)
	cfg, err := Load(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.Interval != 15*time.Second || cfg.Timeout != time.Second || len(cfg.Include) != 2 || cfg.OTLP.Endpoint != "otel-gateway.internal:4317" ||
		cfg.OTLP.Headers["authorization"] != "Bearer x" || cfg.Prometheus.Listen != ":9558" || cfg.Prometheus.Path != "/metrics" ||
		cfg.Resource["deployment.environment"] != "prod" || cfg.Units["io.example.Queue"] != "queue.service" || cfg.Concurrency != 8 ||
		cfg.ScrapeOptions().ManagerSocket != "/run/user/1000/systemd/io.systemd.Manager" {
		t.Errorf("config = %+v", cfg)
	}

	empty := filepath.Join(t.TempDir(), "empty.yaml")
	os.WriteFile(empty, nil, 0o644)
	if cfg, err := Load(empty); err != nil || cfg.Interval != 30*time.Second {
		t.Errorf("empty file: %+v, %v", cfg, err)
	}

	unknown := filepath.Join(t.TempDir(), "unknown.yaml")
	os.WriteFile(unknown, []byte("intervall: 5s\n"), 0o644)
	if _, err := Load(unknown); err == nil {
		t.Error("unknown key accepted")
	}

	for name, mutate := range map[string]func(*Config){
		"interval":    func(c *Config) { c.Interval = 0 },
		"timeout":     func(c *Config) { c.Timeout = time.Minute },
		"concurrency": func(c *Config) { c.Concurrency = -1 },
		"naming":      func(c *Config) { c.Naming = "fancy" },
		"pattern":     func(c *Config) { c.Include = []string{"["} },
		"protocol":    func(c *Config) { c.OTLP.Protocol = "udp" },
		"compression": func(c *Config) { c.OTLP.Compression = "zstd" },
		"path":        func(c *Config) { c.Prometheus.Path = "metrics" },
	} {
		c := Default()
		mutate(&c)
		if err := c.Validate(); err == nil {
			t.Errorf("%s: invalid config accepted", name)
		}
	}
}

func TestPrint(t *testing.T) {
	dir := startProvider(t)
	res, err := scrape.New(scrape.Options{Dir: dir, Logger: quiet}).Scrape(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var text bytes.Buffer
	if err := Print(&text, res, "text"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"SOURCE", "io.example.Queue.Depth", "result=ok", `"busy"`, "1 sources, 4 values"} {
		if !strings.Contains(text.String(), want) {
			t.Errorf("text output lacks %q:\n%s", want, text.String())
		}
	}
	var js bytes.Buffer
	if err := Print(&js, res, "json"); err != nil {
		t.Fatal(err)
	}
	var out struct {
		Sources []jsonSource `json:"sources"`
	}
	if err := json.Unmarshal(js.Bytes(), &out); err != nil || len(out.Sources) != 1 || len(out.Sources[0].Families) != 4 {
		t.Errorf("json output: %v\n%s", err, js.String())
	}
	if err := Print(io.Discard, res, "yaml"); err == nil {
		t.Error("unknown format accepted")
	}
}
