// Command queued is the sample daemon for the provider SDK: a toy job
// queue that publishes its metrics through systemd's report framework.
//
// Run it directly (it binds /run/systemd/report/io.example.Queue, so it
// needs write access there; use -dir to pick another directory):
//
//	go run ./examples/queued -dir /tmp/report
//	go run ./cmd/sdmetrics-exporter -once -dir /tmp/report
//
// Or install it with the unit files next to this file, socket-activated,
// and read it with systemd-report:
//
//	sudo install -m755 queued /usr/local/bin/
//	sudo cp queued.socket queued.service /etc/systemd/system/
//	sudo systemctl enable --now queued.socket
//	systemd-report metrics io.example.Queue
package main

import (
	"context"
	"flag"
	"log"
	"math/rand/v2" // nosemgrep: go.lang.security.audit.crypto.math_random.math-random-used
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/rossedman/sdmetrics/provider"
)

// main runs a worker queue that publishes its depth, throughput and worker
// state as systemd metrics, socket-activated when started by queued.socket.
func main() {
	dir := flag.String("dir", provider.DefaultDir, "report directory, when not socket-activated")
	workers := flag.Int("workers", 3, "worker goroutines")
	flag.Parse()

	p, err := provider.New("io.example.Queue",
		provider.WithDir(*dir),
		provider.FromActivation(),      // adopt queued.socket when started by it
		provider.WithSocketMode(0o666), // like systemd's own sources: readable by any local user
	)
	if err != nil {
		log.Fatal(err)
	}

	depth := p.Gauge("Depth", provider.Help("Jobs waiting"))
	jobs := p.Counter("JobsProcessed", provider.Help("Jobs finished, by result"), provider.Labels("result"))
	busy := p.Gauge("WorkersBusy", provider.Help("Workers running a job"))
	state := p.String("WorkerState", provider.Help("Per worker metric: state"))
	p.GaugeFunc("Goroutines", func() float64 { return float64(runtime.NumGoroutine()) }, provider.Help("Goroutines in the daemon"))
	p.StringFunc("GoVersion", runtime.Version, provider.Help("Go release the daemon was built with"))

	ok, failed := jobs.With("ok"), jobs.With("failed") // keep handles on hot paths

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	queue := make(chan time.Duration, 1000)
	go func() { // producer
		for ctx.Err() == nil {
			select {
			case queue <- time.Duration(rand.IntN(300)) * time.Millisecond:
				depth.Set(float64(len(queue)))
			default:
			}
			time.Sleep(time.Duration(rand.IntN(200)) * time.Millisecond)
		}
	}()
	for w := 0; w < *workers; w++ {
		name := "worker-" + string(rune('a'+w))
		ws := state.On(name)
		ws.Set("idle")
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case job := <-queue:
					depth.Set(float64(len(queue)))
					busy.Inc()
					ws.Set("busy")
					time.Sleep(job)
					if rand.IntN(10) == 0 {
						failed.Inc()
					} else {
						ok.Inc()
					}
					busy.Dec()
					ws.Set("idle")
				}
			}
		}()
	}

	log.Printf("queued: publishing %s", p.Name())
	if err := p.Serve(ctx); err != nil {
		log.Fatal(err)
	}
}
