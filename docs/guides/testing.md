---
description: Test code that publishes or consumes systemd report metrics in go test, without systemd and without root, and run the integration suite against real systemd.
---

# Testing

Nothing in sdmetrics needs systemd to be tested. A provider serves on any
directory, the scraper reads any directory, and the mapper takes plain
structs. Only the integration suite needs a booted systemd.

## Assert values directly

Every metric handle has a `Value` method, so most tests of instrumented code
need no socket at all:

```go
func TestJobsCounted(t *testing.T) {
    p, err := provider.New("io.example.Queue", provider.WithDir(t.TempDir()))
    if err != nil {
        t.Fatal(err)
    }
    jobs := p.Counter("JobsProcessed", provider.Labels("result"))

    runJob(jobs) // your code under test

    if got := jobs.With("ok").Value(); got != 1 {
        t.Errorf("JobsProcessed{result=ok} = %d, want 1", got)
    }
}
```

`New` binds nothing, so this test touches no file system. Registration
panics on invalid names, which makes a test that constructs your metrics a
cheap guard against names systemd would drop.

## Serve and scrape

To test what a consumer actually sees, serve the provider on a temporary
directory and read it with the scraper:

```go
func TestPublished(t *testing.T) {
    dir, err := os.MkdirTemp("", "report") // (1)!
    if err != nil {
        t.Fatal(err)
    }
    t.Cleanup(func() { os.RemoveAll(dir) })

    p, err := provider.New("io.example.Queue", provider.WithDir(dir))
    if err != nil {
        t.Fatal(err)
    }
    p.Gauge("Depth", provider.Help("Jobs waiting")).Set(42)

    ctx, cancel := context.WithCancel(context.Background())
    done := make(chan error, 1)
    go func() { done <- p.Serve(ctx) }()
    t.Cleanup(func() {
        cancel()
        if err := <-done; err != nil {
            t.Errorf("Serve: %v", err)
        }
    })

    s := scrape.New(scrape.Options{Dir: dir, NoActivationLookup: true}) // (2)!
    var src scrape.Source
    for deadline := time.Now().Add(5 * time.Second); ; { // (3)!
        res, err := s.Scrape(context.Background())
        if err == nil && len(res.Sources) == 1 && res.Sources[0].Err == nil {
            src = res.Sources[0]
            break
        }
        if time.Now().After(deadline) {
            t.Fatalf("provider never answered: %v", err)
        }
        time.Sleep(10 * time.Millisecond)
    }

    f := src.Families[0]
    if f.Name != "io.example.Queue.Depth" || f.Type != scrape.TypeGauge || f.Metrics[0].Value.Number() != 42 {
        t.Errorf("got %+v", f)
    }
}
```

1.  Socket paths are limited to 107 bytes on Linux, and the provider wants
    some room below that to bind atomically. `t.TempDir()` includes the test
    name and can get long; a short `os.MkdirTemp("", ...)` path is safer.
2.  There is no service manager to ask in a test.
3.  `Serve` runs in a goroutine; wait until the socket answers.

The source's `Unit` is whatever unit `go test` runs in, such as your user
session's. To pin it, point `Options.ProcRoot` at a fake `/proc`:

```go
proc := t.TempDir()
pidDir := filepath.Join(proc, strconv.Itoa(os.Getpid()))
os.MkdirAll(pidDir, 0o755)
os.WriteFile(filepath.Join(pidDir, "cgroup"), []byte("0::/system.slice/queued.service\n"), 0o644)

s := scrape.New(scrape.Options{Dir: dir, ProcRoot: proc, NoActivationLookup: true})
// res.Sources[0].Unit == "queued.service"
```

### Raw Varlink

For tests at the wire level, sdvarlink's generated client reads the socket
directly:

```go
import "github.com/rossedman/sdvarlink/systemd/v260/metrics"

c, err := metrics.DialPath(ctx, p.Path())
if err != nil {
    t.Fatal(err)
}
defer c.Close()
for m, err := range c.List(ctx) {
    if err != nil {
        t.Fatal(err) // errors.Is(err, metrics.ErrNoSuchMetric) for an empty provider
    }
    t.Logf("%s %s %s", m.Name, m.Fields, m.Value)
}
```

## Test a mapping

`otelmap` takes a `scrape.Result`, so mapping tests need no sockets. Set
`HostName` and `HostID` to keep resources deterministic:

```go
func TestMapping(t *testing.T) {
    m := otelmap.NewMapper(otelmap.Options{Naming: otelmap.NamingOTel, HostName: "host1", HostID: "0123"})
    res := &scrape.Result{
        Time: time.Now(),
        Sources: []scrape.Source{{
            Name: "io.example.Queue",
            Unit: "queued.service",
            Families: []scrape.Family{{
                Name: "io.example.Queue.LatencySeconds",
                Type: scrape.TypeGauge,
                Metrics: []scrape.Metric{{Value: scrape.Value{Kind: scrape.KindFloat, Float: 0.25}}},
            }},
        }},
    }
    b := m.Map(res)
    got := b.Resources[0].Metrics[0]
    if got.Name != "io.example.queue.latency" || got.Unit != "s" {
        t.Errorf("got %s (%s)", got.Name, got.Unit)
    }
}
```

The last resource of every batch holds the self-metrics.

## Integration tests

`integration/` runs sdmetrics against real systemd: systemd's own sources,
the sample daemon as a service and socket-activated, `systemd-report` reading
it, and the exporter into an in-memory OTLP sink.

```sh
make integration             # on a v260+ host, as root
make integration-container   # in a container booting systemd (docker, cgroup v2)
IMAGE=archlinux:latest make integration-container
```

CI boots `fedora:rawhide` and `archlinux:latest` (both systemd v262 today) with
systemd as PID 1, runs the suite, then installs the packaged unit and checks
`/metrics` and `systemd-analyze security`. The host needs cgroup v2; systemd
v258 and newer refuse cgroup v1.
