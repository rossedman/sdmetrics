---
description: How sdmetrics maps systemd report metrics to OpenTelemetry — types, resource attributes, point attributes, naming modes, counter start times and resets.
---

# OpenTelemetry mapping

The `otelmap` package defines one mapping, used by `sdmetrics-exporter` and
the `systemdreport` receiver alike. A `Mapper` turns each `scrape.Result` into
a `Batch`: **one resource per report source**, plus one resource for
sdmetrics' own self-metrics. `otelmap.ToSDK` converts a batch for the
OpenTelemetry Go SDK; the receiver converts it to Collector `pdata`.

## Types

| systemd type | OpenTelemetry | Value |
| --- | --- | --- |
| <span class="sd-type sd-type--gauge">gauge</span> | Gauge | the number |
| <span class="sd-type sd-type--counter">counter</span> | Sum, monotonic, cumulative | the number; start time is the first observation |
| <span class="sd-type sd-type--string">string</span> | Gauge | `1`, with the string in the `systemd.value` attribute |
| `object` (systemd v262) | not mapped | dropped, counted in `sdmetrics.metrics.dropped{reason="untyped"}` |

Integer values (`scrape.KindInt`) become integer data points, others double
data points. When the Go SDK conversion meets a metric with both, it uses
doubles for all of its points.

A family with no values in a scrape produces no metric. Families without a
known type never reach the mapper: the scraper drops and counts them.

## Resource attributes

Each source's resource carries:

| Attribute | Value | Present |
| --- | --- | --- |
| `host.name` | the hostname | always (override with `Options.HostName`) |
| `host.id` | `/etc/machine-id` (or `/var/lib/dbus/machine-id`) | when readable (override with `Options.HostID`) |
| your `resource:` keys | as configured, sorted by key | when configured |
| `systemd.report.source` | the socket's file name, such as `io.systemd.Manager` | always |
| `systemd.unit.name` | the unit owning the socket, such as `init.scope` | when known, or set with `units:` |
| `process.pid` | the socket's peer PID (int) | when the scraper resolved the unit itself and the source is not socket-activated |
| `service.name` | derived from the unit, see below | always |

`service.name` is, in order of preference:

1. the source's entry in `service_names:`;
2. `systemd` for `init.scope`;
3. the unit name without `.service` or `.socket`, and without a template
   instance: `getty@tty1.service` → `getty`;
4. any other unit name as it is;
5. the source name, when the unit is unknown.

The self-metrics resource carries `host.name`, `host.id`, your `resource:`
keys and `service.name="sdmetrics"`.

!!! info "Proposed attributes"

    `systemd.report.source`, `systemd.unit.name`, `systemd.object` and
    `systemd.value` are proposals in OpenTelemetry semantic-convention style.
    They are not part of the conventions yet; proposing them is on the
    [roadmap](../project/roadmap.md#follow-ups).

## Point attributes

| Attribute | From |
| --- | --- |
| each field | the value's `fields`, keys sorted, values as strings |
| `systemd.object` | the value's `object`, when present |
| `systemd.value` | the string, for string families |

```text title="Example"
io.systemd.Manager.NRestarts  object=systemd-networkd.service  value=0
  → Sum  name=io.systemd.Manager.NRestarts  attributes={systemd.object=systemd-networkd.service}  value=0

io.systemd.Manager.UnitActiveState  object=systemd-networkd.service  value="active"
  → Gauge  attributes={systemd.object=systemd-networkd.service, systemd.value=active}  value=1
```

## Naming modes { #naming-modes }

`naming` (`otelmap.Options.Naming`) decides metric names. Descriptions always
come from `Describe`.

`raw` (default, `NamingRaw`)
:   systemd's family names, unchanged: `io.systemd.Manager.NRestarts`. No
    unit.

`otel` (`NamingOTel`)
:   OpenTelemetry style, by `otelmap.OTelName`:

    1. the namespace (everything before the last dot) is lower-cased, and a
       leading `io.systemd` becomes `systemd`; dashes become `_`;
    2. the field name becomes snake_case, keeping acronyms together;
    3. a trailing `Total` word is dropped;
    4. a trailing unit word moves into the unit field.

| Unit word | OTel unit |
| --- | --- |
| `Seconds` | `s` |
| `Milliseconds` | `ms` |
| `Microseconds` | `us` |
| `Nanoseconds` | `ns` |
| `Bytes` | `By` |
| `Ratio` | `1` |
| `Percent` | `%` |

Steps 3 and 4 apply only when a word would remain, so `io.example.A.Seconds`
stays `io.example.a.seconds`.

| systemd family | `otel` name | Unit |
| --- | --- | --- |
| `io.systemd.Manager.UnitsByTypeTotal` | `systemd.manager.units_by_type` | |
| `io.systemd.Manager.NRestarts` | `systemd.manager.n_restarts` | |
| `io.systemd.Network.IPv4AddressState` | `systemd.network.ipv4_address_state` | |
| `io.example.Api.RequestLatencySeconds` | `io.example.api.request_latency` | `s` |
| `io.example.Api.ResponseBytesTotal` | `io.example.api.response` | `By` |
| `io.example.my-app.CPUUsageRatio` | `io.example.my_app.cpu_usage` | `1` |
| `io.example.A.HTTPServer` | `io.example.a.http_server` | |
| `io.example.queue.jobs_processed_total` | `io.example.queue.jobs_processed` | |

Self-metrics are always named `sdmetrics.*`, with units, in both modes.

## Counter start times and resets

Report sources send cumulative counters without a start time. The mapper
keeps one per series (source, family and point attributes):

- A series seen for the first time starts **now**: its first point has
  start time equal to its timestamp.
- If the value **goes down**, or the source's **peer PID changes** (the
  provider restarted), the series restarts: the start time becomes the
  current scrape's.
- A series missing from a scrape keeps its start time for 10 scrapes, so a
  source that fails briefly doesn't reset its counters.

Gauges have no start time. `sdmetrics.metrics.dropped` starts when the
mapper was created.

Because of this, keep **one `Mapper` per pipeline** and reuse it for every
scrape. A mapper is safe for concurrent use.

## Scope

| Producer | Scope name | Version |
| --- | --- | --- |
| `sdmetrics-exporter`, `otelmap.ToSDK` | `github.com/rossedman/sdmetrics` | the exporter's version (the `scopeVersion` argument) |
| `systemdreport` receiver | `github.com/rossedman/sdmetrics/receiver/systemdreportreceiver` | the Collector's build version |

## In Go

```go
m := otelmap.NewMapper(otelmap.Options{
    Naming:       otelmap.NamingOTel,
    Resource:     map[string]string{"deployment.environment": "prod"},
    ServiceNames: map[string]string{"io.example.Queue": "queue"},
    Units:        map[string]string{"io.example.Queue": "queued.service"},
})
b := m.Map(res) // res from scrape.Scraper.Scrape
for _, r := range b.Resources {
    fmt.Println(r.Source, len(r.Metrics)) // r.Source is "" for the self-metrics
}
rms := otelmap.ToSDK(b, "v0.1.0") // []*metricdata.ResourceMetrics
_ = rms
```
