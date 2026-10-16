// Package exporter implements sdmetrics-exporter: it scrapes every
// systemd report source on an interval and pushes the result as OTLP,
// serves it for Prometheus, or both.
package exporter

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/rossedman/sdmetrics/otelmap"
	"github.com/rossedman/sdmetrics/scrape"
)

// Config is the exporter configuration, read from YAML.
type Config struct {
	// Interval between scrapes.
	Interval time.Duration `yaml:"interval"`
	// Timeout per source.
	Timeout time.Duration `yaml:"timeout"`
	// Concurrency is how many sources are scraped at once.
	Concurrency int `yaml:"concurrency"`
	// Dir is the report directory.
	Dir string `yaml:"dir"`
	// Include and Exclude filter sources by name (path.Match patterns).
	Include []string `yaml:"include"`
	Exclude []string `yaml:"exclude"`
	// Naming is "raw" (systemd's family names) or "otel".
	Naming string `yaml:"naming"`
	// ManagerSocket is the service manager's Varlink socket, asked which
	// unit owns a socket-activated source (default
	// /run/systemd/io.systemd.Manager; per-user managers listen on
	// $XDG_RUNTIME_DIR/systemd/io.systemd.Manager).
	ManagerSocket string `yaml:"manager_socket"`

	OTLP       OTLPConfig       `yaml:"otlp"`
	Prometheus PrometheusConfig `yaml:"prometheus"`

	// Resource attributes added to every source's resource.
	Resource map[string]string `yaml:"resource"`
	// ServiceNames overrides service.name per source.
	ServiceNames map[string]string `yaml:"service_names"`
	// Units sets the unit per source, for socket-activated sources whose
	// unit cannot be derived from their peer credentials.
	Units map[string]string `yaml:"units"`
}

// OTLPConfig configures the OTLP push.
type OTLPConfig struct {
	// Endpoint is host:port, or a URL for http/protobuf. Empty disables
	// the push unless OTEL_EXPORTER_OTLP_ENDPOINT or
	// OTEL_EXPORTER_OTLP_METRICS_ENDPOINT is set.
	Endpoint string `yaml:"endpoint"`
	// Protocol is "grpc" (default) or "http/protobuf".
	Protocol string `yaml:"protocol"`
	// Insecure disables TLS.
	Insecure bool `yaml:"insecure"`
	// Headers are sent with every export, for example for authentication.
	Headers map[string]string `yaml:"headers"`
	// Timeout bounds each export.
	Timeout time.Duration `yaml:"timeout"`
	// Compression is "gzip" or "none" (default).
	Compression string `yaml:"compression"`
}

// PrometheusConfig configures the pull endpoint.
type PrometheusConfig struct {
	// Listen is the address to serve on, such as ":9558". Empty disables it.
	Listen string `yaml:"listen"`
	// Path is the metrics path (default /metrics).
	Path string `yaml:"path"`
}

// Default returns the default configuration.
func Default() Config {
	return Config{
		Interval:    30 * time.Second,
		Timeout:     scrape.DefaultTimeout,
		Concurrency: scrape.DefaultConcurrency,
		Dir:         scrape.DefaultDir,
		Naming:      "raw",
		OTLP:        OTLPConfig{Protocol: "grpc", Timeout: 10 * time.Second, Compression: "none"},
		Prometheus:  PrometheusConfig{Path: "/metrics"},
	}
}

// Load reads a YAML file over the defaults. Unknown keys are errors.
func Load(file string) (Config, error) {
	cfg := Default()
	b, err := os.ReadFile(file)
	if err != nil {
		return cfg, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	// An empty file decodes to io.EOF: keep the defaults.
	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return cfg, fmt.Errorf("%s: %w", file, err)
	}
	return cfg, nil
}

// Validate checks the configuration.
func (c *Config) Validate() error {
	var errs []error
	if c.Interval <= 0 {
		errs = append(errs, errors.New("interval must be positive"))
	}
	if c.Timeout <= 0 {
		errs = append(errs, errors.New("timeout must be positive"))
	}
	if c.Timeout > c.Interval {
		errs = append(errs, fmt.Errorf("timeout %v exceeds interval %v", c.Timeout, c.Interval))
	}
	if c.Concurrency <= 0 {
		errs = append(errs, errors.New("concurrency must be positive"))
	}
	if _, err := c.naming(); err != nil {
		errs = append(errs, err)
	}
	for _, p := range append(append([]string{}, c.Include...), c.Exclude...) {
		if _, err := path.Match(p, ""); err != nil {
			errs = append(errs, fmt.Errorf("bad pattern %q", p))
		}
	}
	switch c.OTLP.Protocol {
	case "grpc", "http/protobuf", "http":
	default:
		errs = append(errs, fmt.Errorf("otlp.protocol %q: want grpc or http/protobuf", c.OTLP.Protocol))
	}
	switch c.OTLP.Compression {
	case "", "none", "gzip":
	default:
		errs = append(errs, fmt.Errorf("otlp.compression %q: want gzip or none", c.OTLP.Compression))
	}
	if c.Prometheus.Path == "" || !strings.HasPrefix(c.Prometheus.Path, "/") {
		errs = append(errs, fmt.Errorf("prometheus.path %q must start with /", c.Prometheus.Path))
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

// OTLPEnabled reports whether the OTLP push is configured, in the file or
// through the standard OTEL_EXPORTER_OTLP_* variables.
func (c *Config) OTLPEnabled() bool {
	return c.OTLP.Endpoint != "" || os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" || os.Getenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT") != ""
}

// ScrapeOptions returns the scraper options the configuration describes.
func (c *Config) ScrapeOptions() scrape.Options {
	return scrape.Options{
		Dir:           c.Dir,
		Include:       c.Include,
		Exclude:       c.Exclude,
		Concurrency:   c.Concurrency,
		Timeout:       c.Timeout,
		ManagerSocket: c.ManagerSocket,
	}
}

func (c *Config) mapperOptions(version string) otelmap.Options {
	n, _ := c.naming()
	return otelmap.Options{Naming: n, Resource: c.Resource, ServiceNames: c.ServiceNames, Units: c.Units, ScopeVersion: version}
}
