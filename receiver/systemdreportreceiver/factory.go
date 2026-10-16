package systemdreportreceiver // import "github.com/rossedman/sdmetrics/receiver/systemdreportreceiver"

import (
	"context"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/receiver"
	"go.opentelemetry.io/collector/scraper"
	"go.opentelemetry.io/collector/scraper/scraperhelper"

	"github.com/rossedman/sdmetrics/receiver/systemdreportreceiver/internal/metadata"
	"github.com/rossedman/sdmetrics/scrape"
)

// NewFactory creates a factory for the systemdreport receiver.
func NewFactory() receiver.Factory {
	return receiver.NewFactory(
		metadata.Type,
		createDefaultConfig,
		receiver.WithMetrics(createMetricsReceiver, metadata.MetricsStability),
	)
}

func createDefaultConfig() component.Config {
	cc := scraperhelper.NewDefaultControllerConfig()
	cc.CollectionInterval = 30 * time.Second
	return &Config{
		ControllerConfig: cc,
		Directory:        scrape.DefaultDir,
		SourceTimeout:    scrape.DefaultTimeout,
		Concurrency:      scrape.DefaultConcurrency,
		Naming:           "raw",
		SelfMetrics:      true,
	}
}

func createMetricsReceiver(_ context.Context, set receiver.Settings, cfg component.Config, next consumer.Metrics) (receiver.Metrics, error) {
	c := cfg.(*Config)
	s := newReportScraper(c, set)
	sc, err := scraper.NewMetrics(s.scrape)
	if err != nil {
		return nil, err
	}
	return scraperhelper.NewMetricsController(&c.ControllerConfig, set, next, scraperhelper.AddMetricsScraper(metadata.Type, sc))
}
