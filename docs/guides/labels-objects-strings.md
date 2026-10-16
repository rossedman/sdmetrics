---
description: Give values context with labels (fields) and objects, publish states with string metrics, and delete series when what they describe goes away.
---

# Labels, objects & strings

A family often has many values: one per result, per worker, per interface.
systemd's report framework gives each value two kinds of context, and the
provider exposes both.

| Context | On the wire | In the provider | In OpenTelemetry |
| --- | --- | --- | --- |
| **Fields**: dimensions that tell values apart | `"fields":{"result":"ok"}` | `Labels(...)` and `With(...)` | point attributes |
| **Object**: the thing a value is about | `"object":"worker-a"` | `On(...)` | `systemd.object` attribute |

The `List` blocks on this page use a compact notation for the replies:
`name{object}{"field":"value"} value`.

## Labels

Declare label names at registration, then pick a series with `With`, one
value per label, in order:

```go
reqs := p.Counter("Requests", provider.Help("HTTP requests"), provider.Labels("method", "code"))

reqs.With("GET", "200").Inc()
reqs.With("POST", "500").Inc()
```

```text title="List"
io.example.Api.Requests{"code":"200","method":"GET"} 1
io.example.Api.Requests{"code":"500","method":"POST"} 1
```

Label names follow the metric naming rules: a letter, then letters, digits
and single underscores. Declaring an invalid or duplicate name panics, and so
does calling `With` with the wrong number of values.

!!! tip "Keep handles"

    `With` finds the series without a lock or an allocation, but it still
    hashes the values. On hot paths, look the series up once:

    ```go
    get200 := reqs.With("GET", "200")
    // per request:
    get200.Inc()
    ```

Label values are unbounded strings. As with any metrics system, keep their
cardinality low; the scraper keeps at most 16,384 values per source and counts
the rest as dropped.

## Objects

`On(object)` returns the series about one thing, such as a unit, a device or
a worker. systemd uses objects heavily: `io.systemd.Manager.NRestarts` has one
value per unit, with the unit's name as the object.

```go
restarts := p.Counter("WorkerRestarts", provider.Help("Per worker: restarts"))

restarts.On("worker-a").Inc()
restarts.On("worker-b").Add(3)
```

```text title="List"
io.example.Queue.WorkerRestarts{worker-a} 1
io.example.Queue.WorkerRestarts{worker-b} 3
```

`With` and `On` compose, in either order: `With` keeps the receiver's object
and `On` keeps its label values.

```go
errs := p.Counter("WorkerErrors", provider.Labels("kind"))
errs.On("worker-a").With("timeout").Inc()
errs.With("timeout").On("worker-b").Inc()
```

A series appears once it is written. Intermediate handles, such as
`errs.On("worker-a")` before `.With(...)`, are never published.

!!! question "Field or object?"

    Use the **object** when the value is *about* something with its own
    identity, especially a systemd unit, and there is one natural subject per
    value. Use **fields** for dimensions you would group or sum by, such as a
    result code. A value can have both.

## String metrics

A string metric publishes a string value, such as a state or a version.
Series without a value are not published.

```go
state := p.String("WorkerState", provider.Help("Per worker: state"))

ws := state.On("worker-a")
ws.Set("idle")
// later
ws.Set("busy")
```

```text title="List"
io.example.Queue.WorkerState{worker-a} "busy"
```

In OpenTelemetry a string becomes an info-style metric: a Gauge with value
`1` and the string in the `systemd.value` attribute. In Prometheus that is

```text
io_example_Queue_WorkerState{systemd_object="worker-a",systemd_value="busy",...} 1
```

`Set` allocates (it stores a new string); numeric updates do not.

## Deleting series

`Delete` stops publishing a series, for example when the worker or unit it
describes goes away:

```go
ws.Delete()
```

Using the handle afterwards does not bring the series back; call `With` or
`On` again for a fresh one. On the root series of a family (no labels, no
object), `Delete` resets the value and hides it until it is next written.

## A complete example

`examples/queued` is the provider SDK's sample daemon. It uses every feature
on this page, plus callback metrics and socket activation:

```go title="examples/queued/main.go"
--8<-- "examples/queued/main.go"
```

Run it and read it back:

```sh
go run ./examples/queued -dir /tmp/report
go run ./cmd/sdmetrics-exporter -once -dir /tmp/report
```

Next: [Callback metrics & collectors](collectors.md).
