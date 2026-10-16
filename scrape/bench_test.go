package scrape_test

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/rossedman/sdmetrics/provider"
	"github.com/rossedman/sdmetrics/scrape"
)

// fleet starts n providers in one report directory, each with families
// families of series values.
func fleet(tb testing.TB, n, families, series int) (dir, proc string) {
	tb.Helper()
	dir, err := os.MkdirTemp("", "report")
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { os.RemoveAll(dir) })
	proc = tb.TempDir()
	os.MkdirAll(fmt.Sprintf("%s/%d", proc, os.Getpid()), 0o755)
	os.WriteFile(fmt.Sprintf("%s/%d/cgroup", proc, os.Getpid()), []byte("0::/system.slice/bench.service\n"), 0o644)
	ctx, cancel := context.WithCancel(context.Background())
	tb.Cleanup(cancel)
	for i := 0; i < n; i++ {
		p, err := provider.New(fmt.Sprintf("io.example.S%d", i), provider.WithDir(dir), provider.WithLogger(quiet))
		if err != nil {
			tb.Fatal(err)
		}
		for f := 0; f < families; f++ {
			c := p.Counter(fmt.Sprintf("F%d", f), provider.Labels("id"))
			for s := 0; s < series; s++ {
				c.With(strconv.Itoa(s)).Add(uint64(s))
			}
		}
		go p.Serve(ctx)
	}
	// Wait for every socket.
	s := scrape.New(scrape.Options{Dir: dir, ProcRoot: proc, Logger: quiet})
	for deadline := time.Now().Add(10 * time.Second); ; {
		res, err := s.Scrape(context.Background())
		if err == nil && len(res.Sources) == n && failed(res) == 0 {
			break
		}
		if time.Now().After(deadline) {
			tb.Fatalf("fleet did not start: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	return dir, proc
}

func failed(res *scrape.Result) int {
	n := 0
	for _, s := range res.Sources {
		if s.Err != nil {
			n++
		}
	}
	return n
}

// BenchmarkScrape200 scrapes 200 sources of 5 families with 10 values
// each. CI compares it against the base branch.
func BenchmarkScrape200(b *testing.B) {
	dir, proc := fleet(b, 200, 5, 10)
	s := scrape.New(scrape.Options{Dir: dir, ProcRoot: proc, Logger: quiet})
	b.ReportAllocs()
	for b.Loop() {
		res, err := s.Scrape(context.Background())
		if err != nil || len(res.Sources) != 200 || failed(res) > 0 {
			b.Fatalf("scrape: %v", err)
		}
	}
}

// TestScrapeBudget checks the performance target: 50 sources scraped in
// well under a second.
func TestScrapeBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	dir, proc := fleet(t, 50, 5, 20)
	s := scrape.New(scrape.Options{Dir: dir, ProcRoot: proc, Logger: quiet})
	res, err := s.Scrape(context.Background())
	if err != nil || failed(res) > 0 {
		t.Fatalf("scrape: %v", err)
	}
	if res.Duration > time.Second {
		t.Errorf("50 sources took %v, budget is 1s", res.Duration)
	}
	t.Logf("50 sources, 5000 values in %v", res.Duration)
}
