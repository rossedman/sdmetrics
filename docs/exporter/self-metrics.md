---
description: Watch the exporter and your report sources with sdmetrics' self-metrics — source health, unit resolution, dropped values — with example alert rules.
---

# Self-metrics

Every batch ends with one extra resource, `service.name="sdmetrics"`, that
describes the scrape itself. It answers the questions you ask when a
dashboard goes blank: did the source answer, how long did it take, could it
be attributed to a unit, and was anything thrown away?

| Metric | Answers |
| --- | --- |
| `sdmetrics.sources` | How many sources are on the host? |
| `sdmetrics.sources.failed` | How many couldn't be scraped this time? |
| `sdmetrics.scrape.duration` | How long did the whole scrape take? |
| `sdmetrics.source.up` | Did *this* source answer? |
| `sdmetrics.source.scrape.duration` | How long did *this* source take? |
| `sdmetrics.source.unit.resolved` | Do we know which unit *this* source belongs to, and if not, why? |
| `sdmetrics.metrics.dropped` | How many values were dropped, from which source, and why? |

Types, units and attributes are in the
[Self-metrics reference](../reference/self-metrics.md). The Collector
receiver emits the same metrics, unless `self_metrics: false`.

## Reading them

```console
$ curl -s 127.0.0.1:9558/metrics | grep '^sdmetrics_'
sdmetrics_scrape_duration{service_name="sdmetrics"} 0.0269
sdmetrics_source_scrape_duration{service_name="sdmetrics",systemd_report_source="io.systemd.Manager"} 0.0081
sdmetrics_source_unit_resolved{reason="activated",service_name="sdmetrics",systemd_report_source="io.example.Queue"} 0
sdmetrics_source_unit_resolved{reason="",service_name="sdmetrics",systemd_report_source="io.systemd.Manager"} 1
sdmetrics_source_up{service_name="sdmetrics",systemd_report_source="io.example.Queue"} 1
sdmetrics_source_up{service_name="sdmetrics",systemd_report_source="io.systemd.Manager"} 1
sdmetrics_sources{service_name="sdmetrics"} 9
sdmetrics_sources_failed{service_name="sdmetrics"} 0
```

The names above are for `naming: raw`. With `naming: otel` the Prometheus
conventions add unit suffixes: `sdmetrics_scrape_duration_seconds`,
`sdmetrics_source_scrape_duration_seconds`, `sdmetrics_metrics_dropped_total`
and so on; `sdmetrics_source_up` keeps its name.

## Example alerts

```yaml title="sdmetrics.rules.yml (naming: raw)"
groups:
  - name: sdmetrics
    rules:
      - alert: ReportSourceDown
        expr: sdmetrics_source_up == 0
        for: 5m
        annotations:
          summary: "{{ $labels.systemd_report_source }} on {{ $labels.instance }} does not answer"

      - alert: ReportSourceSlow
        expr: sdmetrics_source_scrape_duration > 1
        for: 10m
        annotations:
          summary: "{{ $labels.systemd_report_source }} takes over a second (timeout is 2s)"

      - alert: ReportValuesDropped
        expr: increase(sdmetrics_metrics_dropped[1h]) > 0
        annotations:
          summary: "{{ $labels.systemd_report_source }} drops values ({{ $labels.reason }})"

      - alert: ReportSourceUnattributed
        expr: sdmetrics_source_unit_resolved == 0
        for: 1h
        annotations:
          summary: "{{ $labels.systemd_report_source }} has no unit ({{ $labels.reason }}); set units: in the config"
```

## Interpreting them

`sdmetrics.source.up` is 0
:   The source failed: a dial error (often `permission denied` on a `0660`
    socket; see [reading restricted sources](systemd.md#reading-restricted-sources)),
    a timeout, or a protocol error. Run `sdmetrics-exporter -once` to see the
    error, or raise `-log.level debug`.

`sdmetrics.metrics.dropped{reason="untyped"}` grows
:   A source publishes families without a known type. Expected for systemd
    v262 sources that use the new `object` type, such as journald's.

`sdmetrics.metrics.dropped{reason="invalid"}` grows
:   A source sends families not named after it, values that don't match
    their type, or malformed fields. The exporter's log names the family once.

`sdmetrics.metrics.dropped{reason="truncated"}` grows
:   A source sends more than 16,384 values per scrape. Usually a label with
    unbounded cardinality.

`sdmetrics.source.unit.resolved{reason="activated"}`
:   A socket-activated source on systemd v260 or v261. Name it with
    [`units:`](configuration.md#units).

`sdmetrics.source.unit.resolved{reason="proc_unreadable"}`
:   The exporter can't read `/proc/<pid>/cgroup`, because of `hidepid=` or
    `ProtectProc=`. See [Unit identity](../guides/unit-identity.md#when-the-unit-cant-be-resolved).
