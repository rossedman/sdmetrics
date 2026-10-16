// Command sdmetrics-exporter exports every systemd report source on a
// host (systemd v260+) as OpenTelemetry metrics: it scrapes the sockets
// under /run/systemd/report/ on an interval and pushes OTLP over gRPC or
// HTTP, serves /metrics for Prometheus, or both.
//
//	sdmetrics-exporter -config /etc/sdmetrics/exporter.yaml
//	sdmetrics-exporter -otlp.endpoint otel-gateway:4317 -otlp.insecure
//	sdmetrics-exporter -prometheus.listen :9558
//	sdmetrics-exporter -once            # print one scrape and exit
//
// Flags override the configuration file. See
// https://rossedman.github.io/sdmetrics/exporter/ for the full reference.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/rossedman/sdmetrics/internal/exporter"
	"github.com/rossedman/sdmetrics/scrape"
)

// Set by GoReleaser.
var (
	version = ""
	commit  = ""
	date    = ""
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

type listFlag []string

func (l *listFlag) String() string { return strings.Join(*l, ",") }
func (l *listFlag) Set(v string) error {
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			*l = append(*l, p)
		}
	}
	return nil
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("sdmetrics-exporter", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(stderr, "Usage: sdmetrics-exporter [flags]\n\nExport systemd report metrics (systemd v260+) as OpenTelemetry.\n\nFlags:\n")
		fs.PrintDefaults()
	}
	var (
		configFile  = fs.String("config", "", "YAML configuration `file`")
		once        = fs.Bool("once", false, "scrape once, print the result and exit")
		format      = fs.String("format", "text", "output of -once: text or json")
		showVersion = fs.Bool("version", false, "print the version and exit")
		logLevel    = fs.String("log.level", "info", "log level: debug, info, warn or error")
		logFormat   = fs.String("log.format", "text", "log format: text or json")

		interval    = fs.Duration("interval", 0, "time between scrapes (default 30s)")
		timeout     = fs.Duration("timeout", 0, "per-source timeout (default 2s)")
		concurrency = fs.Int("concurrency", 0, "sources scraped at once (default 8)")
		dir         = fs.String("dir", "", "report directory (default /run/systemd/report)")
		naming      = fs.String("naming", "", "metric names: raw or otel (default raw)")
		include     listFlag
		exclude     listFlag

		otlpEndpoint = fs.String("otlp.endpoint", "", "OTLP endpoint, host:port or URL; enables the push")
		otlpProtocol = fs.String("otlp.protocol", "", "OTLP protocol: grpc or http/protobuf (default grpc)")
		otlpInsecure = fs.Bool("otlp.insecure", false, "disable TLS for OTLP")
		promListen   = fs.String("prometheus.listen", "", "serve Prometheus metrics on this `address`, such as :9558")
	)
	fs.Var(&include, "include", "only scrape sources matching these comma-separated patterns, such as io.systemd.*")
	fs.Var(&exclude, "exclude", "skip sources matching these comma-separated patterns")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "sdmetrics-exporter: unexpected arguments %q\n", fs.Args())
		return 2
	}
	if *showVersion {
		fmt.Fprintln(stdout, versionString())
		return 0
	}

	log, err := newLogger(stderr, *logLevel, *logFormat)
	if err != nil {
		fmt.Fprintf(stderr, "sdmetrics-exporter: %v\n", err)
		return 2
	}

	cfg := exporter.Default()
	if *configFile != "" {
		if cfg, err = exporter.Load(*configFile); err != nil {
			log.Error("loading configuration", "err", err)
			return 2
		}
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if set["interval"] {
		cfg.Interval = *interval
	}
	if set["timeout"] {
		cfg.Timeout = *timeout
	}
	if set["concurrency"] {
		cfg.Concurrency = *concurrency
	}
	if set["dir"] {
		cfg.Dir = *dir
	}
	if set["naming"] {
		cfg.Naming = *naming
	}
	if set["include"] {
		cfg.Include = include
	}
	if set["exclude"] {
		cfg.Exclude = exclude
	}
	if set["otlp.endpoint"] {
		cfg.OTLP.Endpoint = *otlpEndpoint
	}
	if set["otlp.protocol"] {
		cfg.OTLP.Protocol = *otlpProtocol
	}
	if set["otlp.insecure"] {
		cfg.OTLP.Insecure = *otlpInsecure
	}
	if set["prometheus.listen"] {
		cfg.Prometheus.Listen = *promListen
	}
	if err := cfg.Validate(); err != nil {
		log.Error("invalid configuration", "err", err)
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if *once {
		so := cfg.ScrapeOptions()
		so.Logger = log
		s := scrape.New(so)
		res, err := s.Scrape(ctx)
		if err != nil {
			log.Error("scrape failed", "err", err)
			return 1
		}
		if err := exporter.Print(stdout, res, *format); err != nil {
			log.Error("printing", "err", err)
			return 1
		}
		return 0
	}

	e, err := exporter.New(ctx, cfg, log, versionString())
	if err != nil {
		log.Error("starting", "err", err)
		return 2
	}
	log.Info("starting sdmetrics-exporter", "version", versionString(), "dir", cfg.Dir, "interval", cfg.Interval.String(), "otlp", cfg.OTLPEnabled(), "prometheus", cfg.Prometheus.Listen)
	switch err := e.Run(ctx); {
	case errors.Is(err, exporter.ErrNoReportDir):
		// Older systemd: nothing will ever appear. Exit cleanly so the unit
		// does not restart in a loop.
		log.Warn("no report directory; this host needs systemd v260 or newer, exiting", "dir", cfg.Dir)
		return 0
	case err != nil:
		log.Error("exporter stopped", "err", err)
		return 1
	}
	log.Info("stopped")
	return 0
}

func newLogger(w io.Writer, level, format string) (*slog.Logger, error) {
	var l slog.Level
	if err := l.UnmarshalText([]byte(level)); err != nil {
		return nil, fmt.Errorf("log.level: %w", err)
	}
	opts := &slog.HandlerOptions{Level: l}
	switch format {
	case "text":
		return slog.New(slog.NewTextHandler(w, opts)), nil
	case "json":
		return slog.New(slog.NewJSONHandler(w, opts)), nil
	}
	return nil, fmt.Errorf("log.format %q: want text or json", format)
}

func versionString() string {
	v, c, d := version, commit, date
	if v == "" {
		v = "(devel)"
		if bi, ok := debug.ReadBuildInfo(); ok {
			if bi.Main.Version != "" {
				v = bi.Main.Version
			}
			for _, s := range bi.Settings {
				switch s.Key {
				case "vcs.revision":
					if len(s.Value) >= 7 {
						c = s.Value[:7]
					}
				case "vcs.time":
					d = s.Value
				}
			}
		}
	}
	out := v
	if c != "" {
		out += " (" + c
		if d != "" {
			if t, err := time.Parse(time.RFC3339, d); err == nil {
				d = t.Format("2006-01-02")
			}
			out += ", " + d
		}
		out += ")"
	}
	return out
}
