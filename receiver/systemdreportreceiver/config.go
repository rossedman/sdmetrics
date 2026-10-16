package systemdreportreceiver // import "github.com/rossedman/sdmetrics/receiver/systemdreportreceiver"

import (
	"errors"
	"fmt"
	"path"
	"time"

	"go.opentelemetry.io/collector/scraper/scraperhelper"

	"github.com/rossedman/sdmetrics/otelmap"
)

// Config configures the systemdreport receiver.
type Config struct {
	scraperhelper.ControllerConfig `mapstructure:",squash"`

	// Directory is the report directory (default /run/systemd/report).
	// Per-user sources live in $XDG_RUNTIME_DIR/systemd/report.
	Directory string `mapstructure:"directory"`
	// Include, if set, limits scraping to sources whose name matches one
	// of these path.Match patterns, such as "io.systemd.*".
	Include []string `mapstructure:"include"`
	// Exclude skips sources whose name matches one of these patterns.
	Exclude []string `mapstructure:"exclude"`
	// SourceTimeout bounds the time spent on each source (default 2s).
	SourceTimeout time.Duration `mapstructure:"source_timeout"`
	// Concurrency is how many sources are scraped at once (default 8).
	Concurrency int `mapstructure:"concurrency"`
	// Naming is "raw" (systemd's family names, the default) or "otel".
	Naming string `mapstructure:"naming"`
	// Resource attributes added to every source's resource.
	Resource map[string]string `mapstructure:"resource"`
	// ServiceNames overrides service.name per source.
	ServiceNames map[string]string `mapstructure:"service_names"`
	// Units sets systemd.unit.name per source, for socket-activated
	// sources whose unit cannot be derived from peer credentials.
	Units map[string]string `mapstructure:"units"`
	// SelfMetrics enables the sdmetrics.* metrics about the scrape itself
	// (default true).
	SelfMetrics bool `mapstructure:"self_metrics"`

	// prevent unkeyed literal initialization
	_ struct{}
}

// Validate checks the configuration.
func (c *Config) Validate() error {
	var errs []error
	if c.SourceTimeout <= 0 {
		errs = append(errs, errors.New("source_timeout must be positive"))
	}
	if c.Concurrency <= 0 {
		errs = append(errs, errors.New("concurrency must be positive"))
	}
	if _, err := c.naming(); err != nil {
		errs = append(errs, err)
	}
	for _, p := range append(append([]string{}, c.Include...), c.Exclude...) {
		if _, err := path.Match(p, ""); err != nil {
			errs = append(errs, fmt.Errorf("bad pattern %q: %w", p, err))
		}
	}
	return errors.Join(errs...)
}

func (c *Config) naming() (otelmap.Naming, error) {
	switch c.Naming {
	case "", "raw":
		return otelmap.NamingRaw, nil
	case "otel":
		return otelmap.NamingOTel, nil
	}
	return 0, fmt.Errorf("naming %q: want raw or otel", c.Naming)
}
