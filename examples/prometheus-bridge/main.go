// Command prometheus-bridge is an HTTP service instrumented with the
// Prometheus client library that also publishes its metrics through
// systemd's report framework, without re-instrumenting: the provider reads
// the same registry the /metrics handler serves.
//
//	go run ./examples/prometheus-bridge -dir /tmp/report
//	curl -s localhost:8080/hello
//	go run ./cmd/sdmetrics-exporter -once -dir /tmp/report
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/rossedman/sdmetrics/provider"
	"github.com/rossedman/sdmetrics/provider/prombridge"
)

func main() {
	dir := flag.String("dir", provider.DefaultDir, "report directory")
	addr := flag.String("listen", "127.0.0.1:8080", "HTTP address")
	flag.Parse()

	// Existing instrumentation, unchanged.
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector())
	requests := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total", Help: "HTTP requests served",
	}, []string{"path", "code"})
	latency := prometheus.NewHistogram(prometheus.HistogramOpts{
		Name: "http_request_duration_seconds", Help: "HTTP request latency", Buckets: prometheus.DefBuckets,
	})
	reg.MustRegister(requests, latency)

	mux := http.NewServeMux()
	mux.HandleFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
		t := prometheus.NewTimer(latency)
		defer t.ObserveDuration()
		requests.WithLabelValues(r.URL.Path, "200").Inc()
		fmt.Fprintln(w, "hello")
	})
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))

	// The bridge: two lines.
	p, err := provider.New("io.example.Api", provider.WithDir(*dir), provider.WithSocketMode(0o666))
	if err != nil {
		log.Fatal(err)
	}
	p.Register(prombridge.New(reg))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		if err := p.Serve(ctx); err != nil {
			log.Fatal(err)
		}
	}()

	srv := &http.Server{Addr: *addr, Handler: mux}
	go func() { <-ctx.Done(); srv.Close() }()
	log.Printf("serving http://%s/hello and %s", *addr, p.Path())
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
