---
description: Run a provider under systemd with socket activation, choose socket modes and groups, and decide who may read your metrics.
---

# Socket activation & permissions

A provider's socket lives in `/run/systemd/report/`, which only root can
write. There are two ways to get it there: let **systemd bind it** (socket
activation, preferred), or run with enough privilege to bind it yourself.

## Socket activation

systemd binds the socket from a `.socket` unit and hands it to your service.
The service then needs no write access to `/run`, and can run as a
`DynamicUser`.

=== "queued.socket"

    ```ini
    --8<-- "examples/queued/queued.socket"
    ```

=== "queued.service"

    ```ini
    --8<-- "examples/queued/queued.service"
    ```

=== "main.go"

    ```go
    p, err := provider.New("io.example.Queue",
        provider.FromActivation(), // adopt queued.socket when started by it
    )
    ```

`FromActivation` adopts the socket systemd passed (`$LISTEN_FDS`). With
several sockets passed, it picks the one whose `FileDescriptorName=` is the
provider name. When the process was *not* socket-activated, `Serve` binds the
socket itself, so the same binary runs both ways.

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now queued.socket
systemd-report metrics io.example.Queue   # starts queued on first read
```

A socket systemd created is systemd's: the provider does not remove it on
shutdown. `RemoveOnStop=yes` in the `.socket` unit cleans it up when the socket
unit stops.

!!! note "Socket activation and unit identity"

    An activated socket's peer credentials are PID 1's, not your service's.
    The scraper then asks the service manager which unit the socket belongs
    to, which works from systemd v262. On v260 and v261 the source stays
    unattributed unless you name it in the exporter's
    [`units:`](../exporter/configuration.md#units). See
    [Unit identity](unit-identity.md#socket-activated-sources).

## Binding the socket yourself

Without activation, `Serve` binds the socket at `WithDir`/`WithSocketPath`. The
process needs write access to the directory (root, for
`/run/systemd/report`). The provider:

- creates the socket with its final mode and owner in a private directory,
  then renames it into place, so it is never reachable with looser
  permissions;
- replaces a stale socket left by a crashed run, but refuses a path where
  another server is still listening;
- removes the socket file when `Serve` returns.

## Permissions

The socket's mode decides who can read your metrics. Anyone who can
`connect(2)` to it can call `List`.

| Mode | Who can read | Use it for |
| --- | --- | --- |
| `0660` (default) | the socket's owner and group | Metrics only the exporter (or root) should see. |
| `0666` | every local user | Public metrics, like systemd's own sources. |

The default is `0660` because a provider cannot know whether its metrics are
sensitive. systemd's own sources are `0666`; journald's (v262) is `0660`,
group `systemd-journal`.

### Restricting to the exporter

The packaged exporter runs as a `DynamicUser` with
`SupplementaryGroups=sdmetrics`, and its sysusers.d file creates the
`sdmetrics` group. Give your socket that group and mode `0660`:

=== "Socket-activated"

    ```ini
    [Socket]
    ListenStream=/run/systemd/report/io.example.Queue
    SocketMode=0660
    SocketGroup=sdmetrics
    ```

=== "Bound by the provider"

    ```go
    p, err := provider.New("io.example.Queue",
        provider.WithSocketGroup("sdmetrics"), // mode stays 0660
    )
    ```

Now `sdmetrics-exporter` and root can read it, and other users get
`permission denied`.

### Making metrics public

```go
p, err := provider.New("io.example.Queue", provider.WithSocketMode(0o666))
```

Or `SocketMode=0666` in the `.socket` unit.

### Socket options

| Option | Effect |
| --- | --- |
| `WithSocketMode(mode)` | The socket file's permission bits (default `0660`). |
| `WithSocketGroup(name)` | The socket's group, looked up by name when `New` runs. |
| `WithSocketOwner(uid, gid)` | Owner and group by ID; `-1` leaves one unchanged. |
| `WithDir(dir)` | Directory to bind in (default `/run/systemd/report`). |
| `WithSocketPath(path)` | Full path; keep the file name equal to the provider name, or consumers drop your families. |
| `WithListener(l)` | Bring your own listener; the socket options above don't apply. |

These options apply only when the provider binds the socket. With socket
activation, the `.socket` unit's `SocketMode=`, `SocketUser=` and
`SocketGroup=` decide.

## Per-user providers

A provider running in a user session can publish to the user's report
directory, which `systemd-report --user` reads:

```go
dir := filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), "systemd", "report")
p, err := provider.New("io.example.Notes", provider.WithDir(dir))
```

Next: [Bridging Prometheus](prometheus-bridge.md).
