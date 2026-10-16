# Security policy

## Supported versions

Security fixes go into the latest minor release. Before v1.0, that is the
newest `v0.x` tag.

## Reporting a vulnerability

Please report vulnerabilities privately through
[GitHub security advisories](https://github.com/rossedman/sdmetrics/security/advisories/new),
not in public issues. Include a description, affected versions and, if you
can, a reproducer. You can expect an acknowledgement within a week.

## Security model

**The scraper and exporter only read.** They call `io.systemd.Metrics.List`
and `Describe` on report sockets and, for socket-activated sources,
`io.systemd.Unit.List` on the service manager; they never write to a
source beyond those calls.

- **Per-source limits.** Each source has a timeout (default 2 s), a frame
  size limit (1 MiB per reply), a cap on values (16384), and receives no
  file descriptors, so a slow or hostile provider cannot stall the batch
  or exhaust memory. A failing source never fails the others.
- **Strict names.** Families must be named after the socket they come
  from, as systemd-report requires, so one source cannot pose as another.
- **Least privilege.** The packaged unit runs as a dynamic user with no
  capabilities, a read-only file system, and `/run` reduced to the report
  directory, the manager's socket and the resolver's files
  (`systemd-analyze security`: 1.5). Reading other users' `/proc/<pid>/cgroup`
  resolves units; under `hidepid=` or `ProtectProc=` that fails, and the
  scraper falls back to the socket name and records the gap in
  `sdmetrics.source.unit.resolved`.

**The provider SDK exposes only what you register.**

- Sockets are created mode `0660` by default, owner and group
  configurable, so only intended consumers can read a daemon's metrics.
  They are created with their final mode in a private directory and
  renamed into place.
- The server accepts small calls only (64 KiB), no file descriptors, a
  bounded number of connections, and closes idle ones.
- Metric updates never take a lock the server holds.

Bugs in these guarantees are security bugs; please report them as above.
