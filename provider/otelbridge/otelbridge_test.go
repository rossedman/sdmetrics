package otelbridge_test

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

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/rossedman/sdmetrics/provider"
	"github.com/rossedman/sdmetrics/provider/otelbridge"
	"github.com/rossedman/sdmetrics/scrape"
)

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
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	defer mp.Shutdown(context.Background())
	meter := mp.Meter("test")

	ctx := context.Background()
	reqs, _ := meter.Int64Counter("http.server.requests", metric.WithDescription("Requests"))
	reqs.Add(ctx, 3, metric.WithAttributes(attribute.String("http.route", "/a"), attribute.Int("code", 200)))
	inflight, _ := meter.Int64UpDownCounter("http.server.active_requests")
	inflight.Add(ctx, 2)
	temp, _ := meter.Float64Gauge("temperature")
	temp.Record(ctx, 21.5)
	dur, _ := meter.Float64Histogram("http.server.request.duration", metric.WithUnit("s"), metric.WithExplicitBucketBoundaries(0.1, 1))
	dur.Record(ctx, 0.05)
	dur.Record(ctx, 3)
	bytes, _ := meter.Float64Counter("rx", metric.WithUnit("By"))
	bytes.Add(ctx, 1.5)

	got := roundTrip(t, otelbridge.New(reader))
	want := []string{
		`io.example.Api.http_server_active_requests{} gauge 2`,
		`io.example.Api.http_server_request_duration_seconds_bucket{le=+Inf} counter 2`,
		`io.example.Api.http_server_request_duration_seconds_bucket{le=0.1} counter 1`,
		`io.example.Api.http_server_request_duration_seconds_bucket{le=1} counter 1`,
		`io.example.Api.http_server_request_duration_seconds_count{} counter 2`,
		`io.example.Api.http_server_request_duration_seconds_sum{} counter 3.05`,
		`io.example.Api.http_server_requests{code=200,http.route=/a} counter 3`,
		`io.example.Api.rx_bytes{} counter 1.5`,
		`io.example.Api.temperature{} gauge 21.5`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

func TestName(t *testing.T) {
	for _, tc := range [][3]string{
		{"http.server.request.duration", "s", "http_server_request_duration_seconds"},
		{"latency_seconds", "s", "latency_seconds"},
		{"process.memory.usage", "By", "process_memory_usage_bytes"},
		{"jobs", "{job}", "jobs"},
	} {
		if got := otelbridge.Name(tc[0], tc[1]); got != tc[2] {
			t.Errorf("Name(%q, %q) = %q, want %q", tc[0], tc[1], got, tc[2])
		}
	}
}
