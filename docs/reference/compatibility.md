---
description: Which systemd, Go, platform, OpenTelemetry and Collector versions sdmetrics supports, and what changed in systemd v262.
---

# Compatibility

## systemd

| systemd | Status | Notes |
| --- | --- | --- |
| < v260 | not supported | No report framework. The exporter logs once and exits 0; the receiver reports nothing; `scrape` returns `ErrNoReportDir`. |
| v260, v261 | supported | Types `counter`, `gauge`, `string`. Socket-activated sources can't be attributed automatically: set [`units`](../exporter/configuration.md#units). |
| v262 | supported, tested in CI | Socket-activated sources resolve through the manager. The new `object` type is not mapped yet. |

### What changed in systemd v262

| Change | Effect on sdmetrics |
| --- | --- |
| Most of systemd's own sources are socket-activated (`systemd-report-basic.socket` and others, several with `Accept=yes`). | Their peer is PID 1. The scraper asks the manager and reports the triggered service, or the `.socket` unit for `Accept=yes`. |
| `io.systemd.Unit.List` includes each socket unit's `Listen=` addresses. | Makes that lookup possible. |
| New sources: `io.systemd.Basic`, `io.systemd.CGroup`, `io.systemd.Files`, `io.systemd.Journal`, `io.systemd.Report.SignPlain`. | Scraped like any other. journald's is mode `0660`, group `systemd-journal`. |
| A fourth family type, `object` (journald uses it). | Dropped and counted: `sdmetrics.metrics.dropped{reason="untyped"}`. On the [roadmap](../project/roadmap.md#follow-ups). |
| `systemd-report describe-metrics` became `systemd-report describe`. | Documentation only; sdmetrics speaks Varlink directly. |

sdmetrics uses sdvarlink's generated `systemd/v260/metrics` bindings: the
v260 wire format, which newer systemd releases still speak.

## Go and platforms

| Component | Supported |
| --- | --- |
| Go | 1.26 or newer; CI tests the two newest releases, today 1.26 and 1.27 |
| OS | Linux |
| Architectures | amd64, arm64 (CI runs unit tests on both) |
| cgo | not used; `CGO_ENABLED=0` |
| cgroups | v2 for unit identity; the v1 `name=systemd` hierarchy is parsed as a fallback. Integration CI hosts need cgroup v2. |

## Dependencies

| Module | Version | Used by |
| --- | --- | --- |
| `github.com/rossedman/sdvarlink` | v0.2.0 | everything: Varlink client and server |
| `go.opentelemetry.io/otel` (and `sdk/metric`, OTLP exporters) | v1.47.0 | `otelmap.ToSDK`, `otelbridge`, the exporter |
| `github.com/prometheus/client_golang` | v1.25.0 | `prombridge`, the exporter's `/metrics` |
| `go.opentelemetry.io/collector/*` | v0.163.0 / v1.69.0 | the receiver module only |

The receiver is a separate Go module, so importing `provider` or `scrape`
never pulls in the Collector.

## Tested environments

CI boots `fedora:rawhide` and `archlinux:latest`, both on systemd v262 today,
with systemd as PID 1 in a privileged container, and runs the integration
suite: systemd's own sources, the sample daemon as a service and
socket-activated, `systemd-report` reading it, the exporter into an OTLP sink,
and the packaged unit end to end. It runs on every push and pull request and
nightly.

## Stability

| Part | Stability |
| --- | --- |
| `provider`, `scrape`, `otelmap`, bridges | Pre-1.0: minor releases may change APIs; the changelog says how. |
| Exporter configuration and flags | Pre-1.0, changes noted in the changelog. |
| `systemdreport` receiver | Collector stability **alpha**. |
| `systemd.*` attribute names | Proposals, not yet OpenTelemetry semantic conventions. |
