// Command otel-bridge is a worker instrumented with the OpenTelemetry Go
// SDK that publishes its metrics through systemd's report framework. A
// ManualReader on the MeterProvider is all the bridge needs; other
// readers, such as an OTLP push, can sit next to it.
//
//	go run ./examples/otel-bridge -dir /tmp/report
//	go run ./cmd/sdmetrics-exporter -once -dir /tmp/report
package main

import (
	"context"
	"flag"
	"log"
	"math/rand/v2" // nosemgrep: go.lang.security.audit.crypto.math_random.math-random-used
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/rossedman/sdmetrics/provider"
	"github.com/rossedman/sdmetrics/provider/otelbridge"
)

// main bridges an OpenTelemetry meter provider to a systemd report socket.
func main() {
	dir := flag.String("dir", provider.DefaultDir, "report directory")
	flag.Parse()

	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	defer mp.Shutdown(context.Background())
	otel.SetMeterProvider(mp)

	p, err := provider.New("io.example.Worker", provider.WithDir(*dir), provider.WithSocketMode(0o666))
	if err != nil {
		log.Fatal(err)
	}
	p.Register(otelbridge.New(reader))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		if err := p.Serve(ctx); err != nil {
			log.Fatal(err)
		}
	}()

	// Existing instrumentation, unchanged.
	meter := otel.Meter("example/worker")
	tasks, _ := meter.Int64Counter("worker.tasks", metric.WithDescription("Tasks processed"))
	dur, _ := meter.Float64Histogram("worker.task.duration", metric.WithUnit("s"), metric.WithDescription("Task duration"))
	log.Printf("publishing %s", p.Path())
	for ctx.Err() == nil {
		d := time.Duration(rand.IntN(500)) * time.Millisecond
		time.Sleep(d)
		kind := []string{"resize", "encode"}[rand.IntN(2)]
		tasks.Add(ctx, 1, metric.WithAttributes(attribute.String("kind", kind)))
		dur.Record(ctx, d.Seconds(), metric.WithAttributes(attribute.String("kind", kind)))
	}
}
