---
description: Every sdmetrics-exporter configuration key and command-line flag, with defaults, validation rules and examples.
---

# Configuration reference

`sdmetrics-exporter` reads an optional YAML file (`-config`) over its
built-in defaults, then applies flags, which win. Every key is optional.
Unknown keys are errors, so a typo fails at startup instead of being ignored.

```yaml title="A complete configuration"
interval: 30s
timeout: 2s
concurrency: 8
dir: /run/systemd/report
include: ["io.systemd.*", "io.example.*"]
exclude: ["io.systemd.Report.SignPlain"]
naming: otel

otlp:
  endpoint: otel-gateway.internal:4317
  protocol: grpc
  insecure: false
  headers:
    authorization: "Bearer <token>"
  timeout: 10s
  compression: gzip

prometheus:
  listen: "127.0.0.1:9558"
  path: /metrics

resource:
  deployment.environment: prod
service_names:
  io.example.Queue: queue
units:
  io.example.Queue: queued.service
```

## Scraping

| Key | Flag | Default | Description |
| --- | --- | --- | --- |
| `interval` | `-interval` | `30s` | Time between scrapes. Must be positive. |
| `timeout` | `-timeout` | `2s` | Time budget per source. Must be positive and not exceed `interval`. |
| `concurrency` | `-concurrency` | `8` | Sources scraped at once. Must be positive. |
| `dir` | `-dir` | `/run/systemd/report` | The report directory. |
| `include` | `-include` | all sources | Only scrape sources matching one of these patterns. |
| `exclude` | `-exclude` | none | Skip sources matching one of these patterns. |
| `naming` | `-naming` | `raw` | `raw` keeps systemd's family names; `otel` converts them. See [naming modes](../reference/mapping.md#naming-modes). |
| `manager_socket` | — | `/run/systemd/io.systemd.Manager` | The service manager asked which unit owns a socket-activated source. For a per-user report directory, use `$XDG_RUNTIME_DIR/systemd/io.systemd.Manager`. |

Durations use Go syntax: `500ms`, `30s`, `2m`.

### `include` and `exclude` { #include-exclude }

Patterns use Go's `path.Match` syntax and match the source name: `*` matches
any run of characters, `?` one character, `[...]` a class. A malformed
pattern is a configuration error.

```yaml
include: ["io.systemd.*"]          # only systemd's own sources
exclude: ["io.systemd.Network"]    # ...except networkd's
```

On the command line, both flags take comma-separated lists and may be
repeated: `-include 'io.systemd.*,io.example.*'`.

## OTLP

The push is enabled when `otlp.endpoint` is set, or when the
`OTEL_EXPORTER_OTLP_ENDPOINT` or `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`
environment variable is. The OpenTelemetry SDK exporters read the other
standard `OTEL_EXPORTER_OTLP_*` variables too; keys set here take precedence.

| Key | Flag | Default | Description |
| --- | --- | --- | --- |
| `otlp.endpoint` | `-otlp.endpoint` | empty (disabled) | `host:port`, or a URL such as `https://otlp.example.com:4318/v1/metrics`. |
| `otlp.protocol` | `-otlp.protocol` | `grpc` | `grpc` or `http/protobuf` (`http` is accepted as an alias). |
| `otlp.insecure` | `-otlp.insecure` | `false` | Disable TLS. |
| `otlp.headers` | — | none | Headers sent with every export, for example for authentication. |
| `otlp.timeout` | — | `10s` | Bound on each export. |
| `otlp.compression` | — | `none` | `gzip` or `none`. |

Each source is exported as its own `ResourceMetrics`. A failed export is
logged once as `delivery failing` and retried with the next scrape; nothing is
queued.

## Prometheus

| Key | Flag | Default | Description |
| --- | --- | --- | --- |
| `prometheus.listen` | `-prometheus.listen` | empty (disabled) | Address to serve on, such as `:9558` or `127.0.0.1:9558`. |
| `prometheus.path` | — | `/metrics` | The metrics path. Must start with `/`. |

The packaged `/etc/sdmetrics/exporter.yaml` sets `listen: "127.0.0.1:9558"`.
See [Prometheus endpoint](prometheus.md).

## Resources

These keys shape the OpenTelemetry resources. They apply to OTLP and to the
Prometheus labels alike.

### `resource`

Attributes added to every resource, the self-metrics resource included:

```yaml
resource:
  deployment.environment: prod
  cloud.region: eu-west-1
```

### `service_names`

Overrides `service.name` per source. By default it is derived from the unit
(`systemd-networkd.service` → `systemd-networkd`, `init.scope` → `systemd`),
or is the source name when the unit is unknown.

```yaml
service_names:
  io.example.Queue: queue
```

### `units` { #units }

Sets `systemd.unit.name` per source, and with it the default `service.name`.
Use it for socket-activated sources on systemd v260 and v261, where the
manager can't say which unit owns the socket. A source named here also counts
as resolved in `sdmetrics.source.unit.resolved`.

```yaml
units:
  io.example.Queue: queued.service
```

See [Unit identity](../guides/unit-identity.md).

## Other flags

| Flag | Default | Description |
| --- | --- | --- |
| `-config file` | none | YAML configuration file. Without it, the built-in defaults apply. |
| `-once` | `false` | Scrape once, print the result and exit. |
| `-format` | `text` | Output of `-once`: `text` or `json`. |
| `-log.level` | `info` | `debug`, `info`, `warn` or `error`. Per-source failures are logged at `debug`. |
| `-log.format` | `text` | `text` or `json` (`log/slog` handlers). |
| `-version` | — | Print the version and exit. |
| `-help` | — | Print usage and exit. |

`-once` uses `dir`, `include`, `exclude`, `concurrency`, `timeout` and `manager_socket`, and
prints units as the scraper resolved them, before `units` overrides.

## Validation

`sdmetrics-exporter` checks the whole configuration at startup and reports
every problem at once, then exits with status 2:

- `interval`, `timeout` and `concurrency` must be positive, and `timeout` may
  not exceed `interval`;
- `naming` must be `raw` or `otel`;
- every `include` and `exclude` pattern must be valid;
- `otlp.protocol` must be `grpc` or `http/protobuf`, and `otlp.compression`
  `gzip` or `none`;
- `prometheus.path` must start with `/`;
- at least one output must be enabled.

!!! example "The packaged configuration"

    ```yaml title="/etc/sdmetrics/exporter.yaml"
    --8<-- "packaging/config/exporter.yaml"
    ```
