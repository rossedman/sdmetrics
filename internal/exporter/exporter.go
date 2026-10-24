package exporter

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/rossedman/sdmetrics/otelmap"
	"github.com/rossedman/sdmetrics/scrape"
)

// ErrNoReportDir is returned by Run when the report directory is missing,
// as on hosts older than systemd v260. The command exits cleanly on it.
var ErrNoReportDir = scrape.ErrNoReportDir

// pusher is the part of an OTLP exporter Run uses.
type pusher interface {
	Export(context.Context, *metricdata.ResourceMetrics) error
	Shutdown(context.Context) error
}

// Exporter scrapes on an interval and delivers each batch.
type Exporter struct {
	cfg     Config
	log     *slog.Logger
	version string
	scraper *scrape.Scraper
	mapper  *otelmap.Mapper
	push    pusher
	prom    *promCollector
	ready   chan struct{} // closed once the first scrape is delivered
	addr    chan string   // receives the Prometheus address once listening
}

// New builds an exporter. It does not start anything.
func New(ctx context.Context, cfg Config, log *slog.Logger, version string) (*Exporter, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	so := cfg.ScrapeOptions()
	so.Logger = log
	e := &Exporter{
		cfg:     cfg,
		log:     log,
		version: version,
		scraper: scrape.New(so),
		mapper:  otelmap.NewMapper(cfg.mapperOptions(version)),
		ready:   make(chan struct{}),
		addr:    make(chan string, 1),
	}
	if cfg.OTLPEnabled() {
		p, err := newPusher(ctx, cfg.OTLP)
		if err != nil {
			return nil, fmt.Errorf("otlp: %w", err)
		}
		e.push = p
	}
	if cfg.Prometheus.Listen != "" {
		n, _ := cfg.naming()
		e.prom = newPromCollector(n, cfg.Resource)
	}
	if e.push == nil && e.prom == nil {
		return nil, errors.New("nothing to export to: set otlp.endpoint (or OTEL_EXPORTER_OTLP_ENDPOINT) or prometheus.listen")
	}
	return e, nil
}

// newPusher returns an OTLP metric exporter for c, over HTTP or gRPC
// according to its protocol.
func newPusher(ctx context.Context, c OTLPConfig) (pusher, error) {
	// The SDK exporters also read the standard OTEL_EXPORTER_OTLP_*
	// variables; options given here take precedence.
	switch c.Protocol {
	case "http/protobuf", "http":
		opts := []otlpmetrichttp.Option{otlpmetrichttp.WithTimeout(c.Timeout)}
		if c.Endpoint != "" {
			if strings.Contains(c.Endpoint, "://") {
				opts = append(opts, otlpmetrichttp.WithEndpointURL(c.Endpoint))
			} else {
				opts = append(opts, otlpmetrichttp.WithEndpoint(c.Endpoint))
			}
		}
		if c.Insecure {
			opts = append(opts, otlpmetrichttp.WithInsecure())
		}
		if len(c.Headers) > 0 {
			opts = append(opts, otlpmetrichttp.WithHeaders(c.Headers))
		}
		if c.Compression == "gzip" {
			opts = append(opts, otlpmetrichttp.WithCompression(otlpmetrichttp.GzipCompression))
		}
		return otlpmetrichttp.New(ctx, opts...)
	default:
		opts := []otlpmetricgrpc.Option{otlpmetricgrpc.WithTimeout(c.Timeout)}
		if c.Endpoint != "" {
			if strings.Contains(c.Endpoint, "://") {
				opts = append(opts, otlpmetricgrpc.WithEndpointURL(c.Endpoint))
			} else {
				opts = append(opts, otlpmetricgrpc.WithEndpoint(c.Endpoint))
			}
		}
		if c.Insecure {
			opts = append(opts, otlpmetricgrpc.WithInsecure())
		}
		if len(c.Headers) > 0 {
			opts = append(opts, otlpmetricgrpc.WithHeaders(c.Headers))
		}
		if c.Compression == "gzip" {
			opts = append(opts, otlpmetricgrpc.WithCompressor("gzip"))
		}
		return otlpmetricgrpc.New(ctx, opts...)
	}
}

// Run scrapes and delivers until ctx ends. It returns ErrNoReportDir if
// the report directory does not exist at startup.
func (e *Exporter) Run(ctx context.Context) error {
	if _, err := e.scraper.Scrape(ctx); errors.Is(err, scrape.ErrNoReportDir) {
		return ErrNoReportDir
	}
	var srvErr chan error
	if e.prom != nil {
		srv, l, err := e.promServer()
		if err != nil {
			return err
		}
		srvErr = make(chan error, 1)
		go func() { srvErr <- srv.Serve(l) }()
		defer func() {
			// Scrapes are short: give them a moment, then close the rest.
			sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if srv.Shutdown(sctx) != nil {
				srv.Close()
			}
		}()
		e.addr <- l.Addr().String()
		e.log.Info("serving Prometheus metrics", "addr", l.Addr().String(), "path", e.cfg.Prometheus.Path)
	}
	if e.push != nil {
		defer func() {
			sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := e.push.Shutdown(sctx); err != nil {
				e.log.Warn("otlp shutdown", "err", err)
			}
		}()
	}

	tick := time.NewTicker(e.cfg.Interval)
	defer tick.Stop()
	failing := false
	for {
		if err := e.once(ctx); err != nil {
			if !failing {
				e.log.Error("delivery failing", "err", err)
			}
			failing = true
		} else if failing {
			e.log.Info("delivery recovered")
			failing = false
		}
		select {
		case <-ctx.Done():
			return nil
		case err := <-srvErr:
			return fmt.Errorf("prometheus endpoint: %w", err)
		case <-tick.C:
		}
	}
}

// once runs one scrape and delivers it.
func (e *Exporter) once(ctx context.Context) error {
	res, err := e.scraper.Scrape(ctx)
	if err != nil {
		return err
	}
	for _, s := range res.Sources {
		if s.Err != nil {
			e.log.Debug("source failed", "source", s.Name, "err", s.Err)
		}
	}
	b := e.mapper.Map(res)
	if e.prom != nil {
		e.prom.set(b)
	}
	select {
	case <-e.ready:
	default:
		close(e.ready)
	}
	if e.push == nil {
		return nil
	}
	var errs []error
	for _, rm := range otelmap.ToSDK(b, e.version) {
		if err := e.push.Export(ctx, rm); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("otlp export: %d of %d resources failed: %w", len(errs), len(b.Resources), errs[0])
	}
	return nil
}

// promServer binds the Prometheus listen address and returns an HTTP server
// for the metrics path and /healthz, with its listener. The caller serves it.
func (e *Exporter) promServer() (*http.Server, net.Listener, error) {
	reg := prometheus.NewRegistry()
	reg.MustRegister(e.prom, collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	mux := http.NewServeMux()
	mux.Handle(e.cfg.Prometheus.Path, promhttp.HandlerFor(reg, promhttp.HandlerOpts{ErrorLog: slog.NewLogLogger(e.log.Handler(), slog.LevelWarn)}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		select {
		case <-e.ready:
			fmt.Fprintln(w, "ok")
		default:
			http.Error(w, "no scrape yet", http.StatusServiceUnavailable)
		}
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprintf(w, "sdmetrics-exporter %s\nmetrics: %s\n", e.version, e.cfg.Prometheus.Path)
	})
	l, err := net.Listen("tcp", e.cfg.Prometheus.Listen)
	if err != nil {
		return nil, nil, fmt.Errorf("prometheus endpoint: %w", err)
	}
	return &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}, l, nil
}
