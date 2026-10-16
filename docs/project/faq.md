---
description: Frequently asked questions about sdmetrics.
---

# FAQ

??? question "Do I need cgo, libsystemd or systemd-report installed?"

    No. sdmetrics is pure Go and speaks Varlink itself over Unix sockets.
    Binaries build with `CGO_ENABLED=0`. `systemd-report` and `varlinkctl`
    are handy for checking a provider, but not required.

??? question "Which systemd versions are supported?"

    v260 and newer, where the report framework exists. On v260 and v261,
    socket-activated sources need the `units:` setting to be attributed to a
    unit; v262 resolves them automatically. See
    [Compatibility](../reference/compatibility.md).

??? question "What happens on hosts with older systemd?"

    `/run/systemd/report/` doesn't exist. The exporter logs one warning and
    exits with status 0, so its unit doesn't restart in a loop; the receiver
    reports nothing; `scrape.Scrape` returns `scrape.ErrNoReportDir`. A
    provider still serves its socket, but nothing reads it.

??? question "Is this a replacement for node_exporter?"

    No. node_exporter reads the kernel; sdmetrics carries what services
    publish through systemd's report framework, including systemd's own
    metrics. Run both if you need both.

??? question "Why does my provider panic at startup?"

    A metric or label name is one `systemd-report` would drop, or a name is
    registered twice. The panic message names it. See
    [naming rules](../reference/naming.md).

??? question "How do I give a metric a unit?"

    Put it in the name: `LatencySeconds`, `CacheBytes`. The v260 wire format
    has no unit field, so there is no `provider.Unit` option. With
    `naming: otel`, the exporter and receiver move the suffix into the
    OpenTelemetry unit (`io.example.queue.latency`, unit `s`). See
    [Design decisions](design.md#the-provider).

??? question "Why can't the exporter read my provider's socket?"

    The default socket mode is `0660`. Give the socket the `sdmetrics` group
    (`provider.WithSocketGroup("sdmetrics")` or `SocketGroup=sdmetrics`), or
    make it public with mode `0666`. See
    [Socket activation & permissions](../guides/sockets.md#permissions).

??? question "Why is `systemd.unit.name` missing for some sources?"

    Usually the source is socket-activated on systemd v260 or v261, or the
    scraper can't read `/proc/<pid>/cgroup`. `sdmetrics.source.unit.resolved`
    says which, in its `reason` attribute. See
    [Unit identity](../guides/unit-identity.md#when-the-unit-cant-be-resolved).

??? question "Why does a socket-activated source show a `.socket` unit?"

    Its socket unit has `Accept=yes`: systemd starts one service instance per
    connection, so there is no single service to name. The socket unit is
    the stable identity.

??? question "Why are journald's metrics partly missing?"

    Two reasons. Its source (v262) is mode `0660`, group `systemd-journal`:
    add `SupplementaryGroups=systemd-journal` to the exporter with a drop-in.
    And some of its families use the new `object` type, which sdmetrics
    doesn't map yet; those are counted in
    `sdmetrics.metrics.dropped{reason="untyped"}`.

??? question "How do string metrics look in OpenTelemetry and Prometheus?"

    As info metrics: a gauge of `1` with the string in the `systemd.value`
    attribute (`systemd_value` label). See
    [OpenTelemetry mapping](../reference/mapping.md#types).

??? question "Should I use the exporter or the Collector receiver?"

    The exporter is one small hardened binary per host. The receiver fits
    when a Collector already runs on the host, or when you need processors.
    Both produce the same data. See
    [Exporter or receiver?](../receiver/index.md#exporter-or-receiver).

??? question "Can I publish per-user metrics?"

    Yes. Use `provider.WithDir` with `$XDG_RUNTIME_DIR/systemd/report`, and
    read them with `systemd-report --user`, or with the scraper and the
    `Dir` option.

??? question "How fast is it?"

    `Gauge.Set` takes about 38 ns without allocating; a scrape of 50 sources
    with 5,000 values takes about 27 ms. CI fails pull requests that slow the
    benchmarks by more than 25%.

??? question "Does it work on macOS?"

    The report framework is Linux only. Most packages compile elsewhere, but
    peer credentials, and so unit identity, need Linux, and nothing outside
    Linux serves report sources.

??? question "What does the name mean?"

    **s**ystem**d** + **metrics**, a sibling of
    [sdvarlink](https://rossedman.github.io/sdvarlink/).
