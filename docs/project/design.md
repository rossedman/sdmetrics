---
description: The design decisions behind sdmetrics — scope, the provider's API and defaults, unit identity, the OpenTelemetry mapping, packaging — and where the implementation deviates from the original spec.
---

# Design decisions

This page records the decisions that shaped sdmetrics, and why. Where the
implementation differs from the original spec, a note says so.

## Summary

systemd v260's report framework gives every service a standard, local,
discoverable place to publish metrics. It lacked two things for Go and
OpenTelemetry users: a way for Go programs to **publish** into it, and a way to
get **everything in it** into an observability pipeline with proper identity.
sdmetrics provides both, on top of sdvarlink's typed Varlink bindings.

## Goals and non-goals

**Goals**

1. A provider SDK whose updates are as cheap as a Prometheus client's:
   atomic, lock-free, allocation-free.
2. Bridges, so code instrumented with Prometheus or OpenTelemetry joins without
   changes.
3. A scraper that applies exactly `systemd-report`'s rules, and attributes
   every source to its systemd unit.
4. One documented OpenTelemetry mapping, shared by a standalone exporter and
   a Collector receiver.
5. A hardened, packaged exporter that does nothing harmful on hosts without
   the report framework.
6. Pure Go, no cgo, Linux.

**Non-goals**

- Replacing node_exporter or the Collector's host metrics. sdmetrics carries
  what sources publish; it doesn't read `/proc` for metrics of its own.
- A metrics backend, storage or query language.
- Remote transports. Sources are local Unix sockets; delivery is OTLP or
  Prometheus.
- Supporting systemd older than v260.

## The provider

**Names are validated up front, and registration panics.** `systemd-report`
silently drops families not named after their socket. A typo would show up
as missing data in production, so the provider makes it impossible to
register a name systemd would drop, and fails at startup instead.

**Updates are atomics on pre-built series.** Each series holds its value in an
`atomic.Uint64` (float bits for gauges). `With` finds series in a
copy-on-write hash index without a lock; only a new series takes one. `List`
reads snapshots, so consumers never block writers. Result: `Gauge.Set` in
about 38 ns, and `With(...).Inc()` without allocations.

**The default socket mode is `0660`.** systemd's own sources are `0666`, but a
library cannot know whether an application's metrics are sensitive, so it
defaults to the safe choice. `WithSocketMode`, `WithSocketOwner` and
`WithSocketGroup` open it up, and the packaged exporter's `sdmetrics` group
gives a ready-made audience.

**Socket activation is preferred, binding is the fallback.** `FromActivation`
adopts systemd's socket, picked by `FileDescriptorName=`, and falls back to
binding when not activated, so one binary runs both ways. Binding is atomic
(created with its final mode in a private directory, then renamed) and
refuses to replace a live socket.

**An empty provider answers `NoSuchMetric`**, as systemd's own sources do, so
consumers see the same behaviour from Go and C providers.

!!! note "Deviation from the spec: no `provider.Unit` option"

    The original spec had a `provider.Unit(...)` metric option to attach a
    unit (seconds, bytes) to a metric. It was **intentionally not
    implemented**: the v260 wire format has no unit field, so the unit would
    have been invisible to `systemd-report` and every other consumer. Instead,
    the unit goes in the name (`LatencySeconds`, `CacheBytes`), which every
    consumer sees, and the exporter's and receiver's
    [`naming: otel`](../reference/mapping.md#naming-modes) mode moves
    such suffixes into the OpenTelemetry unit field. If a future systemd adds
    a unit to `Describe`, the option can be added without breaking anyone.

## The scraper

**Same rules as `systemd-report`.** Entries must be sockets (or symlinks to
them) named like interfaces; families must be named after their source;
values must match their family's type. What breaks the rules is dropped and
**counted**, never silently: `Invalid`, `Untyped`, `Truncated`, and
`sdmetrics.metrics.dropped` downstream.

**One failing source never fails the batch.** Sources are scraped
concurrently with a per-source timeout; errors stay on their `Source`.

**Bounded by default.** 1 MiB per reply, 16,384 values per source, eight
sources at a time, two seconds each. A misbehaving source can cost a scrape
two seconds and a megabyte, no more.

### Unit identity from the kernel

Sources don't identify themselves, and a self-reported identity could be
spoofed anyway. The socket's `SO_PEERCRED` and the process's cgroup are
facts the kernel vouches for, so the scraper uses those: the innermost unit
on the cgroup path.

**Socket activation needed more.** An activated socket's peer is PID 1, so
cgroups say nothing. From v262, systemd socket-activates most of its own
sources, so this was not an edge case. The scraper asks the manager
(`io.systemd.Unit.List`) which socket unit listens on the source's path, and
reports the service it triggers, or the `.socket` unit for `Accept=yes`. The
answer is cached for about five minutes, since a full unit list is about a
megabyte. That data exists only from systemd v262; on v260 and v261 the
`units:` configuration names activated sources, and
`sdmetrics.source.unit.resolved` makes the gap visible.

## The mapping

**One resource per source.** A source is a unit's metrics, so it maps to an
OpenTelemetry resource with `systemd.unit.name` and a `service.name` derived
from the unit. That makes systemd's own metrics land under `service.name =
systemd` and your daemon's under its own name, without configuration.

**A neutral `Batch` in the middle.** `otelmap` produces plain structs; the
exporter converts them for the Go SDK, the receiver to `pdata`. One mapping,
two front ends, and the receiver doesn't drag the Go SDK into the Collector
or the other way around.

**Raw names by default.** systemd's names are the contract users see in
`systemd-report`; changing them by default would surprise. `naming: otel` is
one switch away.

**Counter start times are tracked.** Report counters carry no start time; the
mapper assigns one on first sight and resets it when a value drops or the
provider's PID changes, so rate calculations survive restarts.

**Strings are info metrics.** A string value becomes a gauge of `1` with the
string as an attribute, the common OpenTelemetry and Prometheus idiom for
states.

**Proposed attribute names.** `systemd.report.source`, `systemd.unit.name`,
`systemd.object` and `systemd.value` follow semantic-convention style, and are
labelled as proposals until they are accepted.

## Packaging

**The exporter exits 0 without a report directory.** Fleets mix systemd
versions. Enabling the unit everywhere should be harmless: on old hosts it
logs once and stops, and `Restart=on-failure` does not loop.

**A hardened unit by default.** `DynamicUser`, an empty read-only `/run` with
only the report directory, the manager socket and the resolver bound in, no
capabilities, a system-call filter: an exposure score of 1.5. `/proc` stays
visible, deliberately, because unit identity needs other processes' cgroups.

**The receiver is a separate module**, built with `mdatagen` and
`scraperhelper` to contrib's conventions, so it can be proposed to
`opentelemetry-collector-contrib` as is, and so library users never download
the Collector.

## Known limitations

- systemd v262's `object` family type is not mapped; such values are dropped
  and counted as `untyped`.
- Exponential histograms bridged through `otelbridge` keep only their sum and
  count.
- On systemd v260 and v261, socket-activated sources need `units:` to be
  attributed.

See the [Roadmap](roadmap.md) for what comes next.
