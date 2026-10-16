# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project
uses [semantic versioning](https://semver.org/). The Collector receiver is
versioned with the root module and tagged `receiver/systemdreportreceiver/vX.Y.Z`.

## [Unreleased]

### Added

- `provider`: publish metrics through systemd's report framework (v260+).
  - Gauges, counters and string metrics; labels (`With`) and objects
    (`On`); callback metrics (`GaugeFunc`, `CounterFunc`, `StringFunc`);
    `Delete` for series that go away.
  - Lock-free, allocation-free updates; reads snapshot atomics.
  - Names validated against systemd's rules at registration.
  - Socket activation (`FromActivation`), socket mode, owner and group,
    and a custom `Collector` interface for other sources.
  - Empty providers answer `io.systemd.Metrics.NoSuchMetric`, as systemd's
    own do.
- `provider/prombridge` and `provider/otelbridge`: publish an existing
  Prometheus registry or OpenTelemetry SDK reader, histograms and
  summaries included.
- `scrape`: discover and scrape every report source concurrently with
  per-source timeouts and limits; resolve each source's unit from its peer
  credentials and cgroup, and for socket-activated sources from the socket
  unit (systemd v262+).
- `otelmap`: map families to OpenTelemetry with resource attributes,
  counter start times and resets, raw or OTel-style naming, and
  self-metrics.
- `sdmetrics-exporter`: OTLP push over gRPC or HTTP, Prometheus
  `/metrics`, `-once` for inspection, YAML configuration (including
  `manager_socket` for per-user scraping); exits cleanly on hosts older
  than systemd v260.
- `systemdreportreceiver`: OpenTelemetry Collector receiver built with
  `mdatagen` and `scraperhelper` (alpha).
- Hardened `sdmetrics-exporter.service`, sysusers.d file and default
  configuration, shipped in deb and rpm packages.
- A documentation site (MkDocs) with guides, the exporter and receiver
  configuration reference and the OpenTelemetry mapping.
- Integration tests against systemd booted as PID 1 (Fedora rawhide, Arch
  Linux), a benchmark gate against the base branch, a 90% coverage minimum,
  and examples: the
  `queued` sample daemon, both bridges and the scraper.
