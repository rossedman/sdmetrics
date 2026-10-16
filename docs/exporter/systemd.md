---
description: The hardened sdmetrics-exporter.service unit — what each setting protects, the sdmetrics group, reading restricted sources such as journald's, and customizing with drop-ins.
---

# Running under systemd

The deb and rpm packages install a hardened unit, a sysusers.d file and a
configuration file. The unit runs the exporter as a dynamic user that sees
almost nothing of the host: `systemd-analyze security` gives it an
exposure score of **1.5** (on a scale where 10 is the worst).

```sh
sudo systemctl enable --now sdmetrics-exporter
systemctl status sdmetrics-exporter
journalctl -u sdmetrics-exporter -f
systemd-analyze security sdmetrics-exporter
```

## The unit

```ini title="/usr/lib/systemd/system/sdmetrics-exporter.service"
--8<-- "packaging/systemd/sdmetrics-exporter.service"
```

### What the exporter can see

The exporter needs very little: the report sockets, the manager's Varlink
socket, `/proc` for unit identity, and the network for OTLP or Prometheus.

| Setting | Effect |
| --- | --- |
| `DynamicUser=yes` | A throwaway UID allocated at start; no account to manage. |
| `SupplementaryGroups=sdmetrics` | Lets it read sources restricted to the `sdmetrics` group. |
| `TemporaryFileSystem=/run:ro` | An empty, read-only `/run`... |
| `BindReadOnlyPaths=-/run/systemd/report -/run/systemd/io.systemd.Manager -/run/systemd/resolve` | ...with only the report directory, the manager's socket (to name socket-activated sources) and the resolver's files (for the OTLP endpoint's name) mapped in. |
| `ProtectSystem=strict`, `ProtectHome=yes`, `PrivateTmp=yes`, `PrivateDevices=yes` | A read-only view of the rest of the system. |
| `ProtectProc=default` (implicit) | `/proc` stays visible, so `/proc/<pid>/cgroup` of other users' processes resolves each source's unit. |
| `CapabilityBoundingSet=`, `NoNewPrivileges=yes` | No capabilities, ever. |
| `RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6` | Unix sockets for sources, IP for delivery. |
| `SystemCallFilter=@system-service ~@privileged @resources` | A narrow system call set. |
| `MemoryDenyWriteExecute=yes`, `LockPersonality=yes`, ... | Standard hardening for a Go binary. |

!!! note "Why not `ProtectProc=invisible`?"

    It would hide other users' processes, and with them the cgroups that tell
    the exporter which unit owns each source. Sources would still be
    scraped, but attributed only by name, with
    `sdmetrics.source.unit.resolved{reason="proc_unreadable"}`.

## The `sdmetrics` group

`/usr/lib/sysusers.d/sdmetrics.conf` creates a system group:

```ini title="/usr/lib/sysusers.d/sdmetrics.conf"
--8<-- "packaging/sysusers.d/sdmetrics.conf"
```

Providers whose metrics only the exporter should read make their socket
`0660` with this group: `provider.WithSocketGroup("sdmetrics")`, or
`SocketGroup=sdmetrics` in a `.socket` unit. See
[Socket activation & permissions](../guides/sockets.md#restricting-to-the-exporter).

## Reading restricted sources { #reading-restricted-sources }

A source whose socket the exporter can't open shows up as a failed source:
`sdmetrics.source.up` is `0` and `-once` prints `error: dial: ...: permission
denied`. Add the socket's group with a drop-in.

systemd v262's journald source, `io.systemd.Journal`, is mode `0660`, group
`systemd-journal`:

```sh
sudo systemctl edit sdmetrics-exporter
```

```ini title="/etc/systemd/system/sdmetrics-exporter.service.d/override.conf"
[Service]
SupplementaryGroups=systemd-journal
```

```sh
sudo systemctl restart sdmetrics-exporter
```

`SupplementaryGroups=` accumulates across drop-ins, so the exporter keeps the
`sdmetrics` group.

!!! warning "journald's `object` families"

    journald publishes some families with the `object` type that systemd v262
    introduced. sdmetrics does not map that type yet, so those values are
    dropped and counted in `sdmetrics.metrics.dropped{reason="untyped"}`;
    journald's counters and gauges come through. See the
    [roadmap](../project/roadmap.md#follow-ups).

## Other customizations

=== "Flags instead of a file"

    ```ini
    [Service]
    ExecStart=
    ExecStart=/usr/bin/sdmetrics-exporter -otlp.endpoint otel-gateway:4317 -naming otel
    ```

=== "Environment"

    ```ini
    [Service]
    Environment=OTEL_EXPORTER_OTLP_ENDPOINT=https://otlp.example.com:4317
    EnvironmentFile=-/etc/sdmetrics/otlp.env
    ```

    Keep secrets such as `OTEL_EXPORTER_OTLP_HEADERS` in the
    `EnvironmentFile`, readable by root only.

=== "TLS client certificates"

    ```ini
    [Service]
    Environment=OTEL_EXPORTER_OTLP_CERTIFICATE=/etc/sdmetrics/tls/ca.pem
    ```

    `ProtectSystem=strict` leaves `/etc` readable. Files under `/run` are
    hidden by `TemporaryFileSystem=/run:ro`; map them in with
    `BindReadOnlyPaths=`.

## On older systemd

On a host without `/run/systemd/report/` (systemd older than v260), the
service logs once and exits with status 0, so `Restart=on-failure` doesn't
loop. Enable it anyway in a fleet image: it starts doing its job after the
host is upgraded and rebooted.

## Uninstalling

Removing the package runs `systemctl disable --now sdmetrics-exporter`; an
upgrade keeps it running and restarts it. Your
`/etc/sdmetrics/exporter.yaml` is kept on upgrade.
