package provider_test

import (
	"context"
	"strconv"
	"testing"

	"github.com/rossedman/sdmetrics/provider"
)

func BenchmarkGaugeSet(b *testing.B) {
	p, _ := provider.New("io.example.Bench", provider.WithDir(b.TempDir()))
	g := p.Gauge("G")
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		g.Set(float64(i))
	}
}

func BenchmarkCounterWithInc(b *testing.B) {
	p, _ := provider.New("io.example.Bench", provider.WithDir(b.TempDir()))
	c := p.Counter("C", provider.Labels("method", "code"))
	b.ReportAllocs()
	for b.Loop() {
		c.With("GET", "200").Inc()
	}
}

func BenchmarkCounterParallel(b *testing.B) {
	p, _ := provider.New("io.example.Bench", provider.WithDir(b.TempDir()))
	c := p.Counter("C", provider.Labels("code"))
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		h := c.With("200")
		for pb.Next() {
			h.Inc()
		}
	})
}

// BenchmarkList measures one List call over 1000 series.
func BenchmarkList(b *testing.B) {
	p, _ := provider.New("io.example.Bench", provider.WithDir(b.TempDir()))
	c := p.Counter("C", provider.Labels("id"))
	for i := 0; i < 1000; i++ {
		c.With(strconv.Itoa(i)).Add(uint64(i))
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Serve(ctx)
	client := waitClient(b, p)
	b.ReportAllocs()
	for b.Loop() {
		for _, err := range client.List(ctx) {
			if err != nil {
				b.Fatal(err)
			}
		}
	}
}
