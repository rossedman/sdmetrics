---
description: Task-focused guides for sdmetrics, each with code that compiles against the current API.
---

# Guides

Each guide covers one task end to end. If you're new, read
[Core concepts](../getting-started/concepts.md) first; it takes five minutes
and explains sources, families, fields and objects.

## Publishing

<div class="grid cards" markdown>

-   :material-gauge:{ .lg .middle } **[Provider SDK](provider.md)**

    ---

    Create a provider, register gauges, counters and strings, serve, and
    shut down. Options and performance.

-   :material-tag-multiple-outline:{ .lg .middle } **[Labels, objects & strings](labels-objects-strings.md)**

    ---

    `Labels` and `With`, `On` for per-object values, string metrics for
    states, and deleting series.

-   :material-function-variant:{ .lg .middle } **[Callback metrics & collectors](collectors.md)**

    ---

    `GaugeFunc`, `CounterFunc`, `StringFunc`, and the `Collector` interface
    for values computed on each read.

-   :material-power-plug-outline:{ .lg .middle } **[Socket activation & permissions](sockets.md)**

    ---

    A `.socket` unit with `FromActivation`, socket modes and groups, and who
    can read your metrics.

-   :simple-prometheus:{ .lg .middle } **[Bridging Prometheus](prometheus-bridge.md)**

    ---

    Publish a `client_golang` registry through a provider, and how its types
    map.

-   :simple-opentelemetry:{ .lg .middle } **[Bridging OpenTelemetry](otel-bridge.md)**

    ---

    Publish an OpenTelemetry SDK reader through a provider, and how names and
    units map.

</div>

## Consuming

<div class="grid cards" markdown>

-   :material-download-network-outline:{ .lg .middle } **[Scraping from Go](scraping.md)**

    ---

    `scrape.Scraper`: options, results, errors and drops, and mapping to
    OpenTelemetry with `otelmap`.

-   :material-card-account-details-outline:{ .lg .middle } **[Unit identity](unit-identity.md)**

    ---

    How each source is attributed to a unit, socket activation, and what to do
    when it can't be.

-   :material-test-tube:{ .lg .middle } **[Testing](testing.md)**

    ---

    Test providers and scrapers in `go test`, without systemd and without
    root.

</div>
