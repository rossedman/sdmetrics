---
description: Reference material for sdmetrics — the OpenTelemetry mapping, systemd's naming rules, self-metrics and version compatibility.
---

# Reference

Exact behaviour, for looking things up. The Go API itself is documented on
[pkg.go.dev](https://pkg.go.dev/github.com/rossedman/sdmetrics).

<div class="grid cards" markdown>

-   :material-map-outline:{ .lg .middle } **[OpenTelemetry mapping](mapping.md)**

    ---

    Types, resource and point attributes, naming modes, counter start times
    and resets.

-   :material-format-letter-case:{ .lg .middle } **[systemd naming rules](naming.md)**

    ---

    What source, family, field and label names systemd accepts, and what
    sdmetrics does with the rest.

-   :material-heart-pulse:{ .lg .middle } **[Self-metrics](self-metrics.md)**

    ---

    Every `sdmetrics.*` metric with its type, unit and attributes.

-   :material-check-decagram-outline:{ .lg .middle } **[Compatibility](compatibility.md)**

    ---

    systemd, Go, platform and dependency versions, and what changed in
    systemd v262.

</div>

## Defaults at a glance

| Setting | Default | Where |
| --- | --- | --- |
| Report directory | `/run/systemd/report` | `provider.DefaultDir`, `scrape.DefaultDir` |
| Provider socket mode | `0660` | `provider.WithSocketMode` |
| Provider idle timeout / max connections | 1 minute / 16 | `WithIdleTimeout`, `WithMaxConnections` |
| Scrape concurrency | 8 | `scrape.DefaultConcurrency` |
| Per-source timeout | 2 s | `scrape.DefaultTimeout` |
| Largest reply | 1 MiB | `scrape.DefaultMaxFrameSize` |
| Values per source | 16,384 | `scrape.DefaultMaxMetrics` |
| Manager socket | `/run/systemd/io.systemd.Manager` | `scrape.DefaultManagerSocket` |
| Socket index cache | ~5 minutes | internal |
| Exporter interval | 30 s | `interval` |
| OTLP protocol / timeout | gRPC / 10 s | `otlp.protocol`, `otlp.timeout` |
| Prometheus path | `/metrics` | `prometheus.path` |
| Receiver interval | 30 s | `collection_interval` |
