---
description: The systemdreport receiver for the OpenTelemetry Collector — what it collects, every configuration key with defaults, and how it differs from sdmetrics-exporter.
---

# systemdreport receiver

`systemdreportreceiver` is an OpenTelemetry Collector receiver for systemd
report metrics (systemd v260+). It scrapes every socket under
`/run/systemd/report/` that serves `io.systemd.Metrics` and emits one
resource per source, identified by its systemd unit. It runs the same
scraper and mapper as `sdmetrics-exporter`, so the data is identical.

| | |
| --- | --- |
| Type | `systemdreport` |
| Signals | metrics |
| Stability | **alpha** |
| Module | `github.com/rossedman/sdmetrics/receiver/systemdreportreceiver` |
| Tags | `receiver/systemdreportreceiver/vX.Y.Z` |
| Built with | `mdatagen` (`metadata.yaml`) and `scraperhelper` |
| Collector | `go.opentelemetry.io/collector` v0.163.0 / v1.69.0 |

The receiver is not in a Collector distribution yet; build one with
[ocb](ocb.md). It is written to the conventions of
`opentelemetry-collector-contrib`, with a view to proposing it there.

## Configuration

```yaml
receivers:
  systemdreport:
    collection_interval: 30s
    naming: otel
    resource:
      deployment.environment: prod

exporters:
  otlp:
    endpoint: otel-gateway.internal:4317

service:
  pipelines:
    metrics:
      receivers: [systemdreport]
      exporters: [otlp]
```

Every key is optional:

| Key | Default | Description |
| --- | --- | --- |
| `collection_interval` | `30s` | Time between scrapes. (The `scraperhelper` default is one minute; this receiver uses 30 s.) |
| `initial_delay` | `1s` | Delay before the first scrape. |
| `timeout` | `0` (none) | Bound on a whole scrape, from `scraperhelper`. |
| `directory` | `/run/systemd/report` | The report directory. Per-user sources live in `$XDG_RUNTIME_DIR/systemd/report`. |
| `include` | all | Only sources matching one of these `path.Match` patterns, such as `io.systemd.*`. |
| `exclude` | none | Skip sources matching one of these patterns. |
| `source_timeout` | `2s` | Time budget per source. Must be positive. |
| `concurrency` | `8` | Sources scraped at once. Must be positive. |
| `manager_socket` | `/run/systemd/io.systemd.Manager` | The service manager asked which unit owns a socket-activated source. Use `$XDG_RUNTIME_DIR/systemd/io.systemd.Manager` with a per-user `directory`. |
| `naming` | `raw` | `raw` or `otel`. See [naming modes](../reference/mapping.md#naming-modes). |
| `resource` | none | Attributes added to every resource. |
| `service_names` | none | `service.name` per source. |
| `units` | none | `systemd.unit.name` per source, for socket-activated sources on systemd v260 and v261. |
| `self_metrics` | `true` | Emit the [`sdmetrics.*` self-metrics](../reference/self-metrics.md) resource. |

A fuller example, with several instances:

```yaml title="receiver/systemdreportreceiver/testdata/config.yaml"
--8<-- "receiver/systemdreportreceiver/testdata/config.yaml"
```

`systemdreport/badnaming` is there to test validation: `naming: fancy` is
rejected with `naming "fancy": want raw or otel`.

## What it emits

One resource per source, with the attributes described in
[OpenTelemetry mapping](../reference/mapping.md#resource-attributes), and
one scope per resource, named
`github.com/rossedman/sdmetrics/receiver/systemdreportreceiver` with the
Collector's build version. Gauges, monotonic cumulative sums with start
times, and string families as gauges of `1` with a `systemd.value`
attribute. Self-metrics come last, on a resource with
`service.name="sdmetrics"`.

## Behaviour

| Situation | What the receiver does |
| --- | --- |
| No `/run/systemd/report/` (systemd < v260) | Logs one warning and reports nothing, without failing the pipeline. |
| One source fails | The others are still reported; `sdmetrics.source.up` is 0 for it, and the error is logged at debug level. |
| Unknown or `object`-typed families | Dropped and counted in `sdmetrics.metrics.dropped`. |

## Permissions

The Collector process needs what the exporter needs: read access to the
sockets (root, the `sdmetrics` group, or `systemd-journal` for journald's
source), `/proc/<pid>/cgroup` of other processes for unit identity, and
`/run/systemd/io.systemd.Manager` for socket-activated sources. Inside a
container, mount `/run/systemd/report` and the manager socket, and run in the
host's PID namespace for `/proc`; otherwise sources are scraped but not
attributed. See [Unit identity](../guides/unit-identity.md).

## Exporter or receiver?

| | `sdmetrics-exporter` | `systemdreport` receiver |
| --- | --- | --- |
| Deploy as | one small static binary per host, deb/rpm | part of a Collector you build |
| Outputs | OTLP gRPC/HTTP, Prometheus | anything the Collector exports |
| Processing | none | processors, batching, retries, queues |
| Hardened unit | included (score 1.5) | yours to write |

Use the exporter when you want the smallest footprint per host; use the
receiver when a Collector already runs on the host.
