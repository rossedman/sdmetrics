---
description: The exporter's Prometheus endpoint — metric names in both naming modes, labels, type mapping, /healthz and a scrape job.
---

# Prometheus endpoint

With `prometheus.listen` set, the exporter serves the **latest batch** at
`prometheus.path` (`/metrics`). A Prometheus scrape reads that batch; it does
not trigger a scrape of the report sources, which keep the exporter's own
`interval`.

```yaml
prometheus:
  listen: "127.0.0.1:9558"   # or ":9558" to expose it beyond the host
  path: /metrics
```

## Endpoints

| Path | Returns |
| --- | --- |
| `/metrics` (`prometheus.path`) | Every source's metrics, the [self-metrics](self-metrics.md), and the exporter's own Go runtime and process metrics (`go_*`, `process_*`). |
| `/healthz` | `200 ok` once the first scrape has been delivered, `503` before. Use it as a readiness check. |
| `/` | A short text page with the version and the metrics path. |

## Names

Metric names are the OpenTelemetry names with every character Prometheus
does not allow replaced by `_`. Which names depends on the
[naming mode](../reference/mapping.md#naming-modes):

| systemd family | `naming: raw` (default) | `naming: otel` |
| --- | --- | --- |
| `io.systemd.Manager.UnitsByTypeTotal` (gauge) | `io_systemd_Manager_UnitsByTypeTotal` | `systemd_manager_units_by_type` |
| `io.systemd.Manager.NRestarts` (counter) | `io_systemd_Manager_NRestarts` | `systemd_manager_n_restarts_total` |
| `io.example.Queue.LatencySeconds` (gauge) | `io_example_Queue_LatencySeconds` | `io_example_queue_latency_seconds` |
| `io.example.Queue.JobsProcessedTotal` (counter) | `io_example_Queue_JobsProcessedTotal` | `io_example_queue_jobs_processed_total` |

In `otel` mode the OpenTelemetry-to-Prometheus conventions apply on the way
out: the unit comes back as a suffix (`s` → `_seconds`, `By` → `_bytes`,
`1` → `_ratio`, and so on) and counters get `_total`. In `raw` mode names are
left alone, so a counter keeps whatever name its source gave it.

!!! note "Bridged histograms"

    Histograms published through `prombridge` or `otelbridge` arrive as
    separate counter families (`..._bucket`, `..._sum`, `..._count`), so
    Prometheus sees counters, not a histogram type. `histogram_quantile` still
    works on the `_bucket` series. In `otel` mode, counters whose names end in
    `_bucket`, `_sum` or `_count` do not get `_total`, so the classic names
    survive.

## Types

| OpenTelemetry | Prometheus |
| --- | --- |
| Monotonic Sum (systemd counter) | `counter` |
| Gauge (systemd gauge) | `gauge` |
| Gauge of `1` (systemd string) | `gauge`, with the string in `systemd_value` |

## Labels

Each sample carries its point attributes and a few resource attributes:

| Label | From |
| --- | --- |
| the value's fields | systemd `fields`, such as `type="service"` |
| `systemd_object` | the value's object, if any |
| `systemd_value` | the string, for string families |
| `systemd_report_source` | the source name |
| `systemd_unit_name` | the source's unit, when known |
| `service_name` | `service.name` |
| keys of `resource:` | your configured resource attributes, dots as `_` |

Host identity (`host.name`, `host.id`) is not copied: Prometheus' own
`instance` and `job` labels already say which host was scraped. `process.pid`
is not copied either, so a restart doesn't create new series.

```text title="Sample output (raw naming, illustrative)"
# HELP io_systemd_Manager_NRestarts Per unit metric: number of restarts
# TYPE io_systemd_Manager_NRestarts counter
io_systemd_Manager_NRestarts{service_name="systemd",systemd_object="systemd-networkd.service",systemd_report_source="io.systemd.Manager",systemd_unit_name="init.scope"} 0
# HELP io_example_Queue_WorkerState Per worker metric: state
# TYPE io_example_Queue_WorkerState gauge
io_example_Queue_WorkerState{service_name="queued",systemd_object="worker-a",systemd_report_source="io.example.Queue",systemd_unit_name="queued.service",systemd_value="busy"} 1
```

Prometheus requires every sample of a metric to have the same label names,
so the exporter merges families of the same name across sources and gives
each sample the union of labels (missing ones empty). If a field collides with
a resource label, the resource label is renamed with a `resource_` prefix. If
two sources produce exactly the same series, the first is kept.

## A scrape job

```yaml title="prometheus.yml"
scrape_configs:
  - job_name: systemd-report
    scrape_interval: 30s        # match the exporter's interval
    static_configs:
      - targets: ["host1:9558", "host2:9558"]
```

Scraping more often than the exporter's `interval` returns the same batch
again. The packaged configuration listens on `127.0.0.1` only; set
`listen: ":9558"` (and open the firewall) to scrape from another machine.

!!! tip "Prefer OTLP for new setups"

    OTLP carries what Prometheus' text format can't: resource attributes
    separate from labels, units, and counter start times. Use the endpoint
    where Prometheus is already the system of record.
