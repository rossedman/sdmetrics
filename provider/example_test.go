package provider_test

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/rossedman/sdvarlink/systemd/v260/metrics"

	"github.com/rossedman/sdmetrics/provider"
)

func Example() {
	dir, _ := os.MkdirTemp("", "report") // in production: the default, /run/systemd/report
	defer os.RemoveAll(dir)

	p, err := provider.New("io.example.Queue", provider.WithDir(dir))
	if err != nil {
		panic(err)
	}
	depth := p.Gauge("Depth", provider.Help("Jobs waiting"))
	jobs := p.Counter("JobsProcessed", provider.Labels("result"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Serve(ctx)

	depth.Set(42)
	jobs.With("ok").Inc()

	// Read it back the way systemd-report does.
	var c *metrics.Client
	for c == nil {
		c, _ = metrics.DialPath(ctx, p.Path())
		time.Sleep(time.Millisecond)
	}
	defer c.Close()
	for m, err := range c.List(ctx) {
		if err != nil {
			panic(err)
		}
		fmt.Println(m.Name, string(m.Fields), string(m.Value))
	}
	// Output:
	// io.example.Queue.Depth  42
	// io.example.Queue.JobsProcessed {"result":"ok"} 1
}
