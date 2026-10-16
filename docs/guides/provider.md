---
description: Publish metrics from Go with the provider package — names, metric types, options, serving, shutdown and performance.
---

# Provider SDK

The `provider` package publishes a Go program's metrics as a systemd report
source. A `Provider` owns one socket and serves `io.systemd.Metrics` on it;
you register metrics on it and update them from anywhere in your program.

```go
p, err := provider.New("io.example.Queue") // socket: /run/systemd/report/io.example.Queue
if err != nil {
    return err
}
depth := p.Gauge("Depth", provider.Help("Jobs waiting"))
jobs := p.Counter("JobsProcessed", provider.Labels("result"))

go p.Serve(ctx) // stops and removes the socket when ctx ends

depth.Set(42)
jobs.With("ok").Inc()
```

## Naming the provider

The provider name is a Varlink interface name in reverse-domain style. It
becomes the socket's file name, and every family is published under it:

| You write | Published as |
| --- | --- |
| `provider.New("io.example.Queue")` | socket `/run/systemd/report/io.example.Queue` |
| `p.Gauge("Depth")` | family `io.example.Queue.Depth` |
| `p.Counter("RequestLatencySeconds")` | family `io.example.Queue.RequestLatencySeconds` |

`New` returns an error for a name that is not a valid interface name.
Registration **panics** on a metric name that `systemd-report` would drop, or
on a name registered twice, so mistakes surface at startup rather than as
silently missing data. Metric names are Varlink field names: a letter, then
letters, digits and single underscores. Use PascalCase, as systemd does.

!!! note "Put the unit in the name"

    The v260 wire format has no unit field. Name metrics with their unit, such
    as `RequestLatencySeconds` or `CacheBytes`. The exporter's
    [`naming: otel`](../reference/otel-mapping.md#naming-modes) mode turns
    that suffix back into an OpenTelemetry unit
    (`io.example.queue.request_latency`, unit `s`).

## Metric types

| Method | Type | Value | Updates |
| --- | --- | --- | --- |
| `p.Gauge(name, ...)` | <span class="sd-type sd-type--gauge">gauge</span> | `float64` | `Set`, `Add`, `Inc`, `Dec`, `Value`, `Delete` |
| `p.Counter(name, ...)` | <span class="sd-type sd-type--counter">counter</span> | `uint64` | `Inc`, `Add(uint64)`, `Value`, `Delete` |
| `p.String(name, ...)` | <span class="sd-type sd-type--string">string</span> | `string` | `Set`, `Value`, `Delete` |
| `p.GaugeFunc(name, fn, ...)` | gauge | `fn() float64` on each read | — |
| `p.CounterFunc(name, fn, ...)` | counter | `fn() uint64` on each read | — |
| `p.StringFunc(name, fn, ...)` | string | `fn() string` on each read | — |
| `p.Register(c)` | any | a `Collector` on each read | — |

Each registration takes `MetricOption`s:

`provider.Help(text)`
:   The family's description, which `Describe` reports and which becomes the
    OpenTelemetry description and the Prometheus `HELP`.

`provider.Labels(names...)`
:   Label names. Each series then needs one value per label, given to `With`.
    See [Labels, objects & strings](labels-objects-strings.md).

A counter or gauge without labels is published from the start, so a counter
reads `0` before its first increment. Series created with `With` or `On` are
published once they are first written. String metrics publish nothing until
`Set` is called.

## Serving

`Serve(ctx)` publishes until `ctx` ends. It:

1. adopts a socket-activated socket, if `FromActivation` was given and systemd
   passed one; otherwise
2. binds the socket itself, with mode `0660` unless an option says otherwise;
3. serves `io.systemd.Metrics.List` and `Describe`, plus
   `org.varlink.service` introspection;
4. on cancellation, closes the socket and, if it bound it, removes the file.

```go
ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
defer stop()

if err := p.Serve(ctx); err != nil {
    log.Fatal(err) // the socket could not be bound or adopted
}
```

`Serve` may be called once; a second call returns an error. Registration may
continue while serving: new metrics appear in the next `List`. A provider with
no values answers `io.systemd.Metrics.NoSuchMetric`, as systemd's own sources
do.

Binding is careful: the socket is created with its final mode and owner and
renamed into place, a stale socket from a previous run is replaced, and a
path where another server is still listening is refused.

## Options

| Option | Default | Effect |
| --- | --- | --- |
| `WithDir(dir)` | `/run/systemd/report` | Directory the socket is created in. Per-user sources live in `$XDG_RUNTIME_DIR/systemd/report`. |
| `WithSocketPath(path)` | `<dir>/<name>` | The socket's full path. Keep the file name equal to the provider name. |
| `WithSocketMode(mode)` | `0660` | The socket's permissions. systemd's own sources use `0666`. |
| `WithSocketOwner(uid, gid)` | unchanged (`-1`) | The socket's owner and group IDs. |
| `WithSocketGroup(group)` | none | The socket's group by name, such as `sdmetrics`. `New` fails if the group does not exist. |
| `FromActivation()` | off | Adopt a socket passed by systemd socket activation; bind as usual when not activated. |
| `WithListener(l)` | none | Serve on your own `net.Listener`; closed when `Serve` returns. |
| `WithLogger(l)` | `slog.Default()` | Logger for serving errors and dropped collector families. |
| `WithServerInfo(info)` | vendor `sdmetrics`, product = name, version from build info | What `org.varlink.service.GetInfo` reports. |
| `WithIdleTimeout(d)` | 1 minute | Close consumer connections idle this long. |
| `WithMaxConnections(n)` | 16 | Concurrent consumer connections. |

The socket options are covered in
[Socket activation & permissions](sockets.md). Two limits are fixed: requests
are capped at 64 KiB (consumers send empty calls) and no file descriptors are
accepted.

```go
p, err := provider.New("io.example.Queue",
    provider.FromActivation(),             // adopt queued.socket when started by it
    provider.WithSocketGroup("sdmetrics"), // otherwise bind it for the exporter's group
    provider.WithLogger(slog.Default().With("component", "metrics")),
)
```

## Concurrency and performance

A `Provider` and every metric handle are safe for concurrent use. Numeric
updates are single atomic operations on the series: they never take a lock
the server holds, and they do not allocate. Reads (`List`) walk copy-on-write
snapshots, so a slow consumer never blocks your hot path.

| Benchmark (`go test -bench . ./provider/`) | Time | Allocations |
| --- | --- | --- |
| `Gauge.Set` | ~38 ns | 0 |
| `Counter.With("GET", "200").Inc()` | — | 0 |

`With` looks the series up in a lock-free hash index; only creating a *new*
series takes a lock and allocates. Still, keep the handle on hot paths:

```go
ok, failed := jobs.With("ok"), jobs.With("failed") // once
// ...
ok.Inc() // per job: one atomic add
```

`String.Set` allocates, since it stores a new string.

CI runs these benchmarks on every pull request, ten times each, and fails if
a median regresses by more than 25% against the base branch
(`hack/benchgate`).

## What a consumer sees

For the snippet at the top of this page, `List` returns:

```text
io.example.Queue.Depth 42
io.example.Queue.JobsProcessed{"result":"ok"} 1
```

Families appear in registration order, and series in creation order, so
output is stable between reads.

Next: [Labels, objects & strings](labels-objects-strings.md).
