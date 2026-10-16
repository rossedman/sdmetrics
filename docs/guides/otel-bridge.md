---
description: Publish metrics from the OpenTelemetry Go SDK through a systemd report provider with a ManualReader, and how instruments, names and units map.
---

# Bridging OpenTelemetry

Code instrumented with the OpenTelemetry Go SDK joins systemd's report
framework through a `ManualReader`: add it to the `MeterProvider`, wrap it in
`otelbridge.New`, register it on a provider.

```go
reader := sdkmetric.NewManualReader()
otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))

p, err := provider.New("io.example.Api")
if err != nil {
    return err
}
p.Register(otelbridge.New(reader))
go p.Serve(ctx)
```

Each `List` or `Describe` call collects from the reader once. Other readers,
such as a periodic OTLP push, can sit next to it on the same
`MeterProvider`: the bridge doesn't replace your pipeline, it adds systemd as
another consumer.

!!! warning "Cumulative temporality"

    The reader must use cumulative temporality, the SDK default. Delta sums
    are published as gauges, since they reset on every collection, and delta
    histograms as gauges too.

## A complete example

`examples/otel-bridge` is a worker instrumented with the OpenTelemetry SDK:

```go title="examples/otel-bridge/main.go"
--8<-- "examples/otel-bridge/main.go"
```

```sh
go run ./examples/otel-bridge -dir /tmp/report &
go run ./cmd/sdmetrics-exporter -once -dir /tmp/report -include io.example.Worker
```

## How instruments map

| OpenTelemetry data | systemd families | Fields |
| --- | --- | --- |
| Sum, monotonic, cumulative (`Counter`, `ObservableCounter`) | counter `name` | attributes |
| Sum, non-monotonic or delta (`UpDownCounter`, ...) | gauge `name` | attributes |
| Gauge (`Gauge`, `ObservableGauge`) | gauge `name` | attributes |
| Histogram, explicit buckets | counter `name_bucket`, counter `name_sum`, counter `name_count` | attributes; cumulative `le` on buckets |
| Exponential histogram | counter `name_sum`, counter `name_count` | attributes |
| Summary | gauge `name`, counter `name_sum`, counter `name_count` | attributes; `quantile` on `name` |

Attribute values are published as their string form. Buckets are made
cumulative, with a final `le="+Inf"`, as in the Prometheus layout.
Exponential histograms keep only their sum and count: the report framework
has no way to carry their buckets.

## How names map

systemd accepts letters, digits and single underscores in a metric name, so
the bridge rewrites OpenTelemetry names:

1. Dots and other characters systemd does not accept become `_`.
2. The units `s`, `ms` and `By` are appended as `_seconds`, `_milliseconds`
   and `_bytes`, unless the name already ends that way.

| Instrument | Unit | Published as |
| --- | --- | --- |
| `http.server.request.duration` | `s` | `io.example.Api.http_server_request_duration_seconds` |
| `worker.tasks` | — | `io.example.Worker.worker_tasks` |
| `worker.task.duration` | `s` | `io.example.Worker.worker_task_duration_seconds_bucket` (+ `_sum`, `_count`) |
| `cache.size` | `By` | `io.example.Api.cache_size_bytes` |

`otelbridge.Name(name, unit)` returns the name the bridge uses, if you need
it elsewhere:

```go
otelbridge.Name("http.server.request.duration", "s") // "http_server_request_duration_seconds"
```

With the exporter's [`naming: otel`](../reference/otel-mapping.md#naming-modes)
the unit suffix moves back into the unit field on the way out:
`io.example.Api.http_server_request_duration_seconds` is exported as
`io.example.api.http_server_request_duration` with unit `s`. The round trip
recovers the unit; the original dots in the instrument name become
underscores.

Next: [Scraping from Go](scraping.md).
