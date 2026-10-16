package systemdreportreceiver

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/receiver/receivertest"

	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/golden"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/pdatatest/pmetrictest"

	"github.com/rossedman/sdmetrics/provider"
	"github.com/rossedman/sdmetrics/receiver/systemdreportreceiver/internal/metadata"
	"github.com/rossedman/sdmetrics/scrape"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// reportDir serves a sample provider in a fresh report directory and
// returns it with a fake /proc placing this process in queued.service.
func reportDir(t *testing.T) (dir, proc string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "report")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(dir) })
	proc = t.TempDir()
	pid := filepath.Join(proc, fmt.Sprint(os.Getpid()))
	require.NoError(t, os.MkdirAll(pid, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pid, "cgroup"), []byte("0::/system.slice/queued.service\n"), 0o644))

	p, err := provider.New("io.example.Queue", provider.WithDir(dir), provider.WithLogger(quiet))
	require.NoError(t, err)
	p.Gauge("Depth", provider.Help("Jobs waiting")).Set(42)
	p.Counter("JobsProcessed", provider.Help("Jobs finished"), provider.Labels("result")).With("ok").Add(7)
	p.String("WorkerState", provider.Help("Per worker state")).On("worker-a").Set("busy")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { p.Serve(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	for i := 0; ; i++ {
		if c, err := net.Dial("unix", p.Path()); err == nil {
			c.Close()
			break
		}
		require.Less(t, i, 1000, "provider did not start")
		time.Sleep(2 * time.Millisecond)
	}
	return dir, proc
}

func newTestScraper(t *testing.T, cfg *Config, proc string) *reportScraper {
	set := receivertest.NewNopSettings(metadata.Type)
	set.BuildInfo.Version = "test"
	s := newReportScraper(cfg, set)
	s.scraper = scrape.New(scrape.Options{Dir: cfg.Directory, ProcRoot: proc, Include: cfg.Include, Exclude: cfg.Exclude,
		Timeout: cfg.SourceTimeout, Concurrency: cfg.Concurrency, Logger: quiet})
	return s
}

var compareOpts = []pmetrictest.CompareMetricsOption{
	pmetrictest.IgnoreTimestamp(),
	pmetrictest.IgnoreStartTimestamp(),
	pmetrictest.IgnoreResourceAttributeValue("host.name"),
	pmetrictest.IgnoreResourceAttributeValue("host.id"),
	pmetrictest.IgnoreResourceAttributeValue("process.pid"),
	pmetrictest.IgnoreMetricValues("sdmetrics.scrape.duration", "sdmetrics.source.scrape.duration"),
	pmetrictest.IgnoreResourceMetricsOrder(),
	pmetrictest.IgnoreMetricsOrder(),
	pmetrictest.IgnoreMetricDataPointsOrder(),
}

func TestScrapeGolden(t *testing.T) {
	for _, tc := range []struct{ naming, golden string }{
		{"raw", "expected_raw.yaml"},
		{"otel", "expected_otel.yaml"},
	} {
		t.Run(tc.naming, func(t *testing.T) {
			dir, proc := reportDir(t)
			cfg := createDefaultConfig().(*Config)
			cfg.Directory = dir
			cfg.Naming = tc.naming
			cfg.Resource = map[string]string{"deployment.environment": "test"}
			got, err := newTestScraper(t, cfg, proc).scrape(context.Background())
			require.NoError(t, err)

			file := filepath.Join("testdata", "scraper", tc.golden)
			if os.Getenv("UPDATE_GOLDEN") != "" {
				require.NoError(t, golden.WriteMetrics(t, file, got))
			}
			want, err := golden.ReadMetrics(file)
			require.NoError(t, err)
			require.NoError(t, pmetrictest.CompareMetrics(want, got, compareOpts...))
		})
	}
}

func TestScrapeNoReportDir(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	cfg.Directory = filepath.Join(t.TempDir(), "missing")
	s := newReportScraper(cfg, receivertest.NewNopSettings(metadata.Type))
	for i := 0; i < 2; i++ {
		md, err := s.scrape(context.Background())
		require.NoError(t, err)
		assert.Equal(t, 0, md.ResourceMetrics().Len())
	}
}

func TestSelfMetricsDisabled(t *testing.T) {
	dir, proc := reportDir(t)
	cfg := createDefaultConfig().(*Config)
	cfg.Directory = dir
	cfg.SelfMetrics = false
	md, err := newTestScraper(t, cfg, proc).scrape(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, md.ResourceMetrics().Len())
	v, _ := md.ResourceMetrics().At(0).Resource().Attributes().Get("systemd.unit.name")
	assert.Equal(t, "queued.service", v.Str())
}

func TestReceiverEndToEnd(t *testing.T) {
	dir, _ := reportDir(t)
	cfg := createDefaultConfig().(*Config)
	cfg.Directory = dir
	cfg.CollectionInterval = 50 * time.Millisecond
	sink := new(consumertest.MetricsSink)
	r, err := NewFactory().CreateMetrics(context.Background(), receivertest.NewNopSettings(metadata.Type), cfg, sink)
	require.NoError(t, err)
	require.NoError(t, r.Start(context.Background(), newMdatagenNopHost()))
	require.Eventually(t, func() bool { return sink.DataPointCount() > 0 }, 10*time.Second, 20*time.Millisecond)
	require.NoError(t, r.Shutdown(context.Background()))

	found := false
	for _, md := range sink.AllMetrics() {
		rms := md.ResourceMetrics()
		for i := 0; i < rms.Len(); i++ {
			if v, ok := rms.At(i).Resource().Attributes().Get("systemd.report.source"); ok && v.Str() == "io.example.Queue" {
				ms := rms.At(i).ScopeMetrics().At(0).Metrics()
				for j := 0; j < ms.Len(); j++ {
					if ms.At(j).Name() == "io.example.Queue.JobsProcessed" {
						assert.Equal(t, pmetric.MetricTypeSum, ms.At(j).Type())
						assert.True(t, ms.At(j).Sum().IsMonotonic())
						found = true
					}
				}
			}
		}
	}
	assert.True(t, found, "JobsProcessed not received")
}
