---
description: Publish values computed on each read with GaugeFunc, CounterFunc and StringFunc, or a whole set of families with the Collector interface.
---

# Callback metrics & collectors

Some values are cheaper to compute when asked than to track: a cache's size,
the number of goroutines, the build's version. The provider calls your code
on each `List` and `Describe` instead.

## Func metrics

Each `Func` variant publishes one value, with no labels and no object:

```go
p.GaugeFunc("Goroutines", func() float64 { return float64(runtime.NumGoroutine()) },
    provider.Help("Goroutines in the daemon"))

p.CounterFunc("CacheEvictions", cache.Evictions, // func() uint64
    provider.Help("Entries evicted from the cache"))

p.StringFunc("GoVersion", runtime.Version,
    provider.Help("Go release the daemon was built with"))
```

| Method | Callback | Notes |
| --- | --- | --- |
| `GaugeFunc(name, fn, opts...)` | `func() float64` | NaN and ±Inf have no JSON form; such reads publish no value. |
| `CounterFunc(name, fn, opts...)` | `func() uint64` | Must not decrease, or consumers see a counter reset. |
| `StringFunc(name, fn, opts...)` | `func() string` | An empty string publishes no value. |

Callbacks run on the server's goroutines, possibly concurrently, so they must
be safe to call concurrently and should be fast: they run inside every
consumer's request. Func metrics take no labels; passing `Labels` panics. When
you need labels, implement a `Collector`.

## Collectors

A `Collector` reports a whole set of families on each read. It is how the
[Prometheus](prometheus-bridge.md) and [OpenTelemetry](otel-bridge.md) bridges
work, and how you bridge anything else:

```go
type Collector interface {
    Collect(ctx context.Context) ([]Family, error)
}

type Family struct {
    Name        string // relative to the provider, such as "RequestsTotal"
    Description string
    Type        Type   // provider.TypeCounter, TypeGauge or TypeString
    Samples     []Sample
}

type Sample struct {
    Object string            // optional
    Fields map[string]string // like labels
    Value  any               // any integer or float type; a string for TypeString
}
```

Here is a collector that publishes per-interface byte counters read from
`/sys/class/net` on each scrape:

```go title="netstat.go"
package main

import (
    "context"
    "os"
    "path/filepath"
    "strconv"
    "strings"

    "github.com/rossedman/sdmetrics/provider"
)

type netStats struct{}

func (netStats) Collect(ctx context.Context) ([]provider.Family, error) {
    rx := provider.Family{Name: "ReceiveBytes", Description: "Bytes received", Type: provider.TypeCounter}
    tx := provider.Family{Name: "TransmitBytes", Description: "Bytes sent", Type: provider.TypeCounter}
    links, err := os.ReadDir("/sys/class/net")
    if err != nil {
        return nil, err
    }
    for _, l := range links {
        for _, f := range []*provider.Family{&rx, &tx} {
            file := "rx_bytes"
            if f == &tx {
                file = "tx_bytes"
            }
            b, err := os.ReadFile(filepath.Join("/sys/class/net", l.Name(), "statistics", file))
            if err != nil {
                continue
            }
            n, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
            if err != nil {
                continue
            }
            f.Samples = append(f.Samples, provider.Sample{Object: l.Name(), Value: n})
        }
    }
    return []provider.Family{rx, tx}, nil
}

func register(p *provider.Provider) {
    p.Register(netStats{})
}
```

### What the provider checks

`Collect` runs on every `List` and `Describe` call, possibly concurrently.
The provider prefixes each family name with its own name and checks every
family, logging each problem **once** per family through the provider's
logger:

| Problem | Result |
| --- | --- |
| `Name` is not a valid field name | family dropped |
| `Name` collides with a metric registered with `Gauge`, `Counter`, ... | family dropped |
| `Type` is not counter, gauge or string | family dropped |
| A value does not fit the type (a string in a gauge, a number in a string family) | sample dropped |
| A float value is NaN or ±Inf | sample dropped |
| `Collect` returns an error | logged; the families it returned are still published |

Returning partial results together with an error is fine: a bridge whose
registry has one failing collector still publishes the rest.

Counter values may be any integer or float type. Integers are published as
integers, as systemd's own counters are; floats keep their fraction.

!!! tip "Collectors vs registered metrics"

    Registered metrics are faster: updates are atomic and `List` reads them
    without calling your code. Use a collector when the values already live
    elsewhere (another library, the kernel, a database) and copying them into
    gauges on a timer would only add staleness.

Next: [Socket activation & permissions](sockets.md).
