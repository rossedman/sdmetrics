---
description: Install the sdmetrics Go packages, the sdmetrics-exporter binary or the Collector receiver, and check that your host has systemd's report framework.
---

# Installation

sdmetrics comes in three pieces. Install the one that fits your job:

| You want to | Install | How |
| --- | --- | --- |
| Publish metrics from a Go program | the Go module | `go get github.com/rossedman/sdmetrics@latest` |
| Export a host's metrics to OTLP or Prometheus | `sdmetrics-exporter` | deb, rpm or tarball from [GitHub Releases](https://github.com/rossedman/sdmetrics/releases) |
| Collect them in the OpenTelemetry Collector | the `systemdreport` receiver | a custom build with [ocb](../receiver/ocb.md) |

## The Go module

```sh
go get github.com/rossedman/sdmetrics@latest
```

The module is pure Go. It needs no cgo, no libsystemd and no daemon, and it
builds with `CGO_ENABLED=0`. It talks Varlink through
[sdvarlink](https://rossedman.github.io/sdvarlink/) v0.2.0, using its generated
`systemd/v260/metrics` client and its `varlink.Server`.

| Import path | Use it for |
| --- | --- |
| `github.com/rossedman/sdmetrics/provider` | Publish metrics from your program: gauges, counters, strings, collectors. |
| `github.com/rossedman/sdmetrics/provider/prombridge` | Publish an existing Prometheus registry. |
| `github.com/rossedman/sdmetrics/provider/otelbridge` | Publish an existing OpenTelemetry SDK reader. |
| `github.com/rossedman/sdmetrics/scrape` | Read every report source on the host from Go. |
| `github.com/rossedman/sdmetrics/otelmap` | Map scrape results to OpenTelemetry. |

The Collector receiver is a **separate module**,
`github.com/rossedman/sdmetrics/receiver/systemdreportreceiver`, so the
libraries above never pull in the Collector's dependencies.

## The exporter

Releases ship `sdmetrics-exporter` for Linux on amd64 and arm64, as a tarball
and as deb and rpm packages.

=== "deb"

    ```sh
    sudo apt install ./sdmetrics-exporter_<version>_linux_amd64.deb
    sudo systemctl enable --now sdmetrics-exporter
    ```

=== "rpm"

    ```sh
    sudo dnf install ./sdmetrics-exporter_<version>_linux_amd64.rpm
    sudo systemctl enable --now sdmetrics-exporter
    ```

=== "tarball"

    ```sh
    tar xzf sdmetrics-exporter_<version>_linux_amd64.tar.gz
    sudo install -m755 sdmetrics-exporter /usr/bin/
    sudo install -Dm644 exporter.yaml /etc/sdmetrics/exporter.yaml
    sudo install -Dm644 systemd/sdmetrics-exporter.service /etc/systemd/system/
    sudo install -Dm644 sysusers.d/sdmetrics.conf /usr/lib/sysusers.d/
    sudo systemd-sysusers sdmetrics.conf
    sudo systemctl daemon-reload
    sudo systemctl enable --now sdmetrics-exporter
    ```

=== "go install"

    ```sh
    go install github.com/rossedman/sdmetrics/cmd/sdmetrics-exporter@latest
    ```

The packages install:

| File | Purpose |
| --- | --- |
| `/usr/bin/sdmetrics-exporter` | The binary. |
| `/usr/lib/systemd/system/sdmetrics-exporter.service` | The hardened unit. See [Running under systemd](../exporter/systemd.md). |
| `/usr/lib/sysusers.d/sdmetrics.conf` | Creates the `sdmetrics` group, for sources only the exporter may read. |
| `/etc/sdmetrics/exporter.yaml` | The configuration, kept on upgrade. See the [reference](../exporter/configuration.md). |

The packages do not start the service: configure it, then run
`systemctl enable --now sdmetrics-exporter`.

## Requirements

| Component | Supported | Notes |
| --- | --- | --- |
| **systemd** | v260 or newer | v260 added the report framework. On older hosts `/run/systemd/report/` does not exist; the exporter logs once and exits cleanly. |
| **systemd for activated sources** | v262 or newer | Needed to attribute socket-activated sources to their unit automatically. See [Unit identity](../guides/unit-identity.md). |
| **Go** | 1.26 or newer | The module tracks the two newest Go releases, today 1.26 and 1.27. |
| **OS** | Linux | The report framework is Linux only. Packages build elsewhere, but peer credentials need Linux. |
| **Architecture** | amd64, arm64 | Tested in CI on both. |
| **cgroups** | v2 | Unit identity reads `/proc/<pid>/cgroup`. The v1 `name=systemd` hierarchy is parsed as a fallback. |
| **cgo** | not used | Static binaries. |

Full version details are in [Compatibility](../reference/compatibility.md).

## Check your host

```sh
systemctl --version | head -1
ls -l /run/systemd/report/
```

```text title="Example output on a systemd v262 host (illustrative)"
systemd 262 (262-1)
srw-rw-rw- 1 root root 0 Oct 16 09:12 io.systemd.Basic
srw-rw-rw- 1 root root 0 Oct 16 09:12 io.systemd.CGroup
srw-rw-rw- 1 root root 0 Oct 16 09:12 io.systemd.Files
srw-rw---- 1 root systemd-journal 0 Oct 16 09:12 io.systemd.Journal
srw-rw-rw- 1 root root 0 Oct 16 09:12 io.systemd.Manager
srw-rw-rw- 1 root root 0 Oct 16 09:12 io.systemd.Network
srw-rw-rw- 1 root root 0 Oct 16 09:12 io.systemd.Report.SignPlain
```

Each socket is a **source**. systemd's own are mode `0666`, so any local user
can read them; journald's is restricted to the `systemd-journal` group.
`systemd-report` reads them:

```sh
systemd-report metrics io.systemd.Manager
systemd-report describe io.systemd.Manager   # describe-metrics on v260 and v261
```

!!! tip "No `/run/systemd/report/`?"

    Your systemd is older than v260. The exporter and the receiver detect this
    and report nothing instead of failing; a provider still serves its socket,
    but nothing on the host will read it.

<div class="grid cards" markdown>

-   :material-play-circle-outline: **Next: publish your first metric**

    ---

    [:octicons-arrow-right-24: Quickstart: publish metrics](quickstart-provider.md)

-   :material-server-outline: **Or: export this host**

    ---

    [:octicons-arrow-right-24: Quickstart: export a host](quickstart-exporter.md)

</div>
