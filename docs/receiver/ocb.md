---
description: Build an OpenTelemetry Collector that includes the systemdreport receiver with the OpenTelemetry Collector Builder (ocb), and run it under systemd.
---

# Building a collector with ocb

The receiver ships as a Go module, not in a Collector distribution. The
[OpenTelemetry Collector Builder](https://opentelemetry.io/docs/collector/custom-collector/)
(`ocb`) compiles a Collector from a list of component modules. You need Go
1.26 or newer.

## 1. Install the builder

Use the builder release that matches the Collector modules you build with,
here v0.163.0:

```sh
go install go.opentelemetry.io/collector/cmd/builder@v0.163.0
```

## 2. Describe the distribution

```yaml title="builder-config.yaml"
dist:
  name: otelcol-systemd
  description: OpenTelemetry Collector with the systemdreport receiver
  output_path: ./otelcol-systemd
  version: 0.1.0

receivers:
  - gomod: github.com/rossedman/sdmetrics/receiver/systemdreportreceiver v0.1.0 # (1)!
  - gomod: go.opentelemetry.io/collector/receiver/otlpreceiver v0.163.0

exporters:
  - gomod: go.opentelemetry.io/collector/exporter/debugexporter v0.163.0
  - gomod: go.opentelemetry.io/collector/exporter/otlpexporter v0.163.0

providers:
  - gomod: go.opentelemetry.io/collector/confmap/provider/envprovider v1.69.0
  - gomod: go.opentelemetry.io/collector/confmap/provider/fileprovider v1.69.0
```

1.  The receiver is tagged separately from the libraries, as
    `receiver/systemdreportreceiver/vX.Y.Z`. Use the newest tag; it is built
    against Collector v0.163.0 / v1.69.0, so keep the other components on
    the same release.

Collector modules come in two version lines: stable modules (`component`,
`confmap`, `pdata`, the confmap providers, ...) are v1.69.0, the rest
v0.163.0. The builder fails if components disagree.

!!! tip "Building from a checkout"

    To build against a local clone of sdmetrics, for example to try an
    unreleased change, point both modules at it:

    ```yaml
    replaces:
      - github.com/rossedman/sdmetrics => /path/to/sdmetrics
      - github.com/rossedman/sdmetrics/receiver/systemdreportreceiver => /path/to/sdmetrics/receiver/systemdreportreceiver
    ```

## 3. Build

```sh
builder --config builder-config.yaml
./otelcol-systemd/otelcol-systemd components | grep -A1 systemdreport
```

`CGO_ENABLED=0` works: nothing in the receiver needs cgo.

## 4. Configure and run

```yaml title="config.yaml"
receivers:
  systemdreport:
    collection_interval: 30s
    naming: otel

exporters:
  debug:
    verbosity: basic
  otlp:
    endpoint: otel-gateway.internal:4317

service:
  pipelines:
    metrics:
      receivers: [systemdreport]
      exporters: [debug, otlp]
```

```sh
sudo ./otelcol-systemd/otelcol-systemd --config config.yaml
```

```text title="Output (illustrative)"
info  MetricsExporter  {"kind": "exporter", "data_type": "metrics", "name": "debug", "resource metrics": 10, "metrics": 212, "data points": 1874}
```

Ten resource metrics: one per source, plus the self-metrics.

## Running it under systemd

A Collector that only scrapes report sources can borrow the exporter's
hardening. Start from
[the exporter's unit](../exporter/systemd.md#the-unit) and change
`ExecStart=`; keep `SupplementaryGroups=sdmetrics`, the
`BindReadOnlyPaths=` for `/run/systemd/report` and
`/run/systemd/io.systemd.Manager`, and the default `ProtectProc=`, so unit
identity keeps working. Add whatever paths your other components need.
