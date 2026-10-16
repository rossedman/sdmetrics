---
description: Publish a Prometheus client_golang registry through a systemd report provider without re-instrumenting, and how Prometheus types and names map.
---

# Bridging Prometheus

Code instrumented with `client_golang` joins systemd's report framework with
two lines: wrap the registry in `prombridge.New` and register it on a
provider. The same registry can keep serving `/metrics`.

```go
p, err := provider.New("io.example.Api")
if err != nil {
    return err
}
p.Register(prombridge.New(prometheus.DefaultGatherer))
go p.Serve(ctx)
```

`prombridge.New` takes any `prometheus.Gatherer`: a `*prometheus.Registry`,
`prometheus.DefaultGatherer`, or a `prometheus.Gatherers` list. Each `List` or
`Describe` call gathers the registry once, so values are always current.

## A complete example

`examples/prometheus-bridge` is an HTTP service instrumented with
`client_golang` that publishes its registry through a provider:

```go title="examples/prometheus-bridge/main.go"
--8<-- "examples/prometheus-bridge/main.go"
```

```sh
go run ./examples/prometheus-bridge -dir /tmp/report &
curl -s localhost:8080/hello
go run ./cmd/sdmetrics-exporter -once -dir /tmp/report -include io.example.Api
```

## How types map

| Prometheus | systemd families | Fields |
| --- | --- | --- |
| Counter `name` | counter `name` | labels |
| Gauge `name` | gauge `name` | labels |
| Untyped `name` | gauge `name` | labels |
| Histogram `name` | counter `name_bucket`, counter `name_sum`, counter `name_count` | labels; `le` on buckets |
| Gauge histogram `name` | the same three, as gauges | labels; `le` on buckets |
| Summary `name` | gauge `name`, counter `name_sum`, counter `name_count` | labels; `quantile` on `name` |

This is the Prometheus text format's own layout, so dashboards built on the
classic histogram series keep working. A `+Inf` bucket is added when the
histogram doesn't report one. Counter values that are whole numbers are
published as integers, like systemd's own counters; others keep their
fraction.

For the example above, the families are:

```text
io.example.Api.http_requests_total                      counter   {code, path}
io.example.Api.http_request_duration_seconds_bucket     counter   {le}
io.example.Api.http_request_duration_seconds_sum        counter
io.example.Api.http_request_duration_seconds_count      counter
io.example.Api.go_goroutines                            gauge
...
```

## How names map

Prometheus names that are valid Varlink field names (a letter, then letters,
digits and single underscores) are kept as they are. Others are sanitized:

| Rule | Example |
| --- | --- |
| Characters other than letters and digits become `_` | `http:requests` → `http_requests` |
| Runs of `_` collapse | `go__gc` → `go_gc` |
| Leading and trailing `_` are dropped | `_internal_` → `internal` |
| A leading digit gets an `m` prefix | `5xx_total` → `m5xx_total` |

Prometheus label names keep their spelling as fields.

With the exporter's [`naming: otel`](../reference/otel-mapping.md#naming-modes),
`io.example.Api.http_requests_total` is exported as
`io.example.api.http_requests` and `..._duration_seconds_sum` as
`io.example.api.http_request_duration_seconds_sum`. The unit suffix is only
recognized as the last word of a name.

!!! tip "Bridge or re-instrument?"

    The bridge costs one `Gather` per scrape and keeps your code unchanged.
    If you are writing new code, or need allocation-free updates on a hot
    path, register metrics on the provider directly. Both can share one
    provider.

Next: [Bridging OpenTelemetry](otel-bridge.md).
