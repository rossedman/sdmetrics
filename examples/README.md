# Examples

Each directory is a runnable program: `go run ./examples/<name>`. The
providers bind their socket under `/run/systemd/report/` by default, which
needs root; pass `-dir /tmp/report` to try them as any user, and read them
back with `go run ./cmd/sdmetrics-exporter -once -dir /tmp/report`.

| Example | Shows |
| --- | --- |
| [queued](queued/) | The provider SDK's sample daemon: gauges, labelled counters, per-object string metrics, callback metrics, and socket activation with the `.socket` and `.service` units next to it |
| [prometheus-bridge](prometheus-bridge/) | An HTTP service instrumented with `client_golang` joining systemd-report through `prombridge`, with no re-instrumentation |
| [otel-bridge](otel-bridge/) | A worker instrumented with the OpenTelemetry SDK joining systemd-report through `otelbridge` and a `ManualReader` |
| [scrape](scrape/) | The `scrape` package on its own: a small systemd-report that also prints each source's unit |

Try two of them together:

```sh
go run ./examples/queued -dir /tmp/report &
go run ./examples/prometheus-bridge -dir /tmp/report &
curl -s localhost:8080/hello
go run ./cmd/sdmetrics-exporter -once -dir /tmp/report
```
