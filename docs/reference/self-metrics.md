---
description: Every sdmetrics.* self-metric emitted by sdmetrics-exporter and the systemdreport receiver, with type, unit, attributes and Prometheus names.
---

# Self-metrics reference

The mapper appends one resource to every batch, with `service.name` set to
`sdmetrics` plus `host.name`, `host.id` and your `resource:` attributes. It
holds these metrics. All of them are integers except the durations.

| Metric | Type | Unit | Attributes | Description |
| --- | --- | --- | --- | --- |
| `sdmetrics.sources` | Gauge | `{source}` | — | Report sources found |
| `sdmetrics.sources.failed` | Gauge | `{source}` | — | Report sources that could not be scraped |
| `sdmetrics.scrape.duration` | Gauge (double) | `s` | — | Time spent scraping all sources |
| `sdmetrics.source.up` | Gauge | — | `systemd.report.source` | 1 if the source was scraped without error, else 0 |
| `sdmetrics.source.scrape.duration` | Gauge (double) | `s` | `systemd.report.source` | Time spent scraping the source |
| `sdmetrics.source.unit.resolved` | Gauge | — | `systemd.report.source`; `reason` when 0 | 1 if the source's unit was resolved, else 0 (reason says why) |
| `sdmetrics.metrics.dropped` | Sum, monotonic, cumulative | `{value}` | `systemd.report.source`, `reason` | Metric values dropped: invalid name or value, unknown type, or over the per-source limit |

The first three are always present. The three per-source metrics appear only
when at least one source was found. `sdmetrics.metrics.dropped` appears once
something has been dropped; it counts from the moment the mapper was created
and is its start time.

## `reason` values

=== "sdmetrics.source.unit.resolved"

    | `reason` | Meaning |
    | --- | --- |
    | `activated` | Socket-activated and the manager didn't name the owning unit (systemd < v262, or the lookup failed). |
    | `proc_unreadable` | `/proc/<pid>/cgroup` could not be read (`hidepid=`, `ProtectProc=`). |
    | `no_unit` | The peer process is not in a systemd unit. |
    | `peer_unavailable` | Peer credentials could not be read, or another resolution error. |
    | `scrape_failed` | The source could not be scraped. |
    | `unknown` | No unit and no recorded reason. |

    A source named in `units:` counts as resolved (`1`) even when the
    scraper could not resolve it.

=== "sdmetrics.metrics.dropped"

    | `reason` | Meaning | `scrape.Source` field |
    | --- | --- | --- |
    | `invalid` | Family not named after its source, value of the wrong kind, or malformed fields. | `Invalid` |
    | `untyped` | `Describe` gave the family no known type, including systemd v262's `object`. | `Untyped` |
    | `truncated` | Beyond 16,384 values from one source in one scrape. | `Truncated` |

## Prometheus names

On the exporter's `/metrics` endpoint, dots become underscores. In `otel`
naming mode the endpoint also adds unit suffixes and `_total`:

| Metric | `naming: raw` | `naming: otel` |
| --- | --- | --- |
| `sdmetrics.sources` | `sdmetrics_sources` | `sdmetrics_sources` |
| `sdmetrics.sources.failed` | `sdmetrics_sources_failed` | `sdmetrics_sources_failed` |
| `sdmetrics.scrape.duration` | `sdmetrics_scrape_duration` | `sdmetrics_scrape_duration_seconds` |
| `sdmetrics.source.up` | `sdmetrics_source_up` | `sdmetrics_source_up` |
| `sdmetrics.source.scrape.duration` | `sdmetrics_source_scrape_duration` | `sdmetrics_source_scrape_duration_seconds` |
| `sdmetrics.source.unit.resolved` | `sdmetrics_source_unit_resolved` | `sdmetrics_source_unit_resolved` |
| `sdmetrics.metrics.dropped` | `sdmetrics_metrics_dropped` (counter) | `sdmetrics_metrics_dropped_total` |

The attribute `systemd.report.source` becomes the label
`systemd_report_source`, and every sample also carries
`service_name="sdmetrics"`.

## In Go

The attribute keys are constants in `otelmap`:

| Constant | Value |
| --- | --- |
| `otelmap.AttrSource` | `systemd.report.source` |
| `otelmap.AttrUnit` | `systemd.unit.name` |
| `otelmap.AttrServiceName` | `service.name` |
| `otelmap.AttrPID` | `process.pid` |
| `otelmap.AttrHostName`, `otelmap.AttrHostID` | `host.name`, `host.id` |
| `otelmap.AttrObject`, `otelmap.AttrValue` | `systemd.object`, `systemd.value` |
| `otelmap.AttrDropReason`, `otelmap.AttrUnitProblem` | `reason` |

In a `Batch`, the self-metrics resource is the last one and has
`Resource.Source == ""`.
