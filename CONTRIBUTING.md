# Contributing to sdmetrics

Thanks for helping. This guide covers how the repository is laid out, how
to run the checks CI runs, and how releases work.

## Quick start

```sh
git clone https://github.com/rossedman/sdmetrics && cd sdmetrics
make test        # unit tests in both modules (no systemd needed)
make race        # with the race detector
make lint        # gofmt, go vet, golangci-lint
make coverage    # fail below 90% statement coverage in either module
make doccheck    # every function, method and package has a doc comment
make vuln        # govulncheck in both modules
make bench       # scraper against 200 fake sources, provider hot paths
make docs-serve  # docs at http://127.0.0.1:8000 (make docs-deps first)
```

You need Go 1.26 or newer, on Linux.

## Layout

| Path | What lives there |
| --- | --- |
| `provider/` | The provider SDK: serve `io.systemd.Metrics` on a report socket |
| `provider/prombridge/`, `provider/otelbridge/` | Bridges from Prometheus registries and OpenTelemetry SDK readers |
| `scrape/` | Discovery, concurrent scraping, unit resolution |
| `otelmap/` | systemd → OpenTelemetry mapping, shared by the exporter and the receiver |
| `internal/exporter/`, `cmd/sdmetrics-exporter/` | The standalone exporter |
| `internal/names/` | systemd's naming rules |
| `receiver/systemdreportreceiver/` | The Collector receiver, **a separate Go module** |
| `packaging/` | systemd unit, sysusers.d file, default config, package scripts |
| `examples/` | Runnable examples, including the SDK sample daemon `queued` |
| `integration/` | Tests against real systemd (`-tags integration`) |
| `hack/` | The container integration runner, the benchmark gate and the doc-comment check |

The receiver module uses `replace github.com/rossedman/sdmetrics => ../..`
during development, so changes to `scrape` or `otelmap` are tested by
both modules at once. Run `make tidy` after changing dependencies in
either.

## Tests

- Unit tests must not need systemd. Use a provider or a `varlinktest`
  fake in a temporary report directory (see `scrape/scrape_test.go`), and
  a fake `/proc` for unit resolution.
- Integration tests need root on a systemd v260+ host and only create
  transient units they remove again:

  ```sh
  sudo make integration             # on the host
  make integration-container        # in a container booting systemd (docker, cgroup v2)
  ```

  CI runs them against `fedora:rawhide` and `archlinux:latest`.
- The receiver's golden files live in
  `receiver/systemdreportreceiver/testdata/scraper/`. After a deliberate
  mapping change, run `make golden` and review the diff.
- Coverage: each module must stay at 90% or more (`make coverage`;
  example programs are excluded). CI fails below that.
- Benchmarks: pull requests compare `scrape` and `provider` benchmarks
  against the base branch, and a median slowdown over 25% fails the check.
- Fuzz targets: `make fuzz FUZZTIME=1m`. Add crashers to `testdata/fuzz`.
- Scanners: [sarifix](https://rossedman.io/sarifix/) runs golangci-lint,
  Trivy, gitleaks and Semgrep (see `sarifix.yaml`). Pull requests get the
  findings they introduce as annotations; on `main`, findings go to code
  scanning and to issues labeled `sarifix`. To run it locally with the
  scanners on your `PATH`: `sarifix scan && sarifix plan --sarif 'sarif/*.sarif'`.
  Intended Trivy findings go in `.trivyignore.yaml`, with a reason.

## Critical paths

These parse untrusted input, run in hot loops or resolve configuration.
Changes to them need a benchmark, and the input-handling ones a fuzz
target; add a new entry when you add such a function.

| Function | Why | Benchmark | Fuzz target |
| --- | --- | --- | --- |
| `scrape.decodeValue` (`scrape/value.go`) | Decodes values from any report socket | `BenchmarkScrape200` | `FuzzDecodeValue` |
| `scrape.decodeFields` (`scrape/value.go`) | Decodes the field maps of every List reply | `BenchmarkScrape200` | `FuzzDecodeFields` |
| `names.SanitizeField` (`internal/names/`) | Turns Prometheus and OpenTelemetry metric names into systemd field names in the bridges | `BenchmarkSanitizeField` | `FuzzSanitizeField` |
| `(*scrape.Scraper).Scrape` / `scrapeOne` | Concurrent scrape of every source | `BenchmarkScrape200` | n/a (covered by the decoders) |
| `provider` metric updates and `List` | Hot path in every instrumented daemon | `BenchmarkGaugeSet`, `BenchmarkCounterWithInc`, `BenchmarkCounterParallel`, `BenchmarkList` | n/a (no external input) |

## Doc comments

Every function and method, exported or not, and every package has a doc
comment starting with its name; generated files and tests are exempt.
`make doccheck` enforces it in CI.

## Claude Code

`.claude/settings.json` enables the
[guardrails](https://github.com/rossedman/guardrails) plugin (format and
lint on edit, a test and coverage gate before Claude stops, configured by
`.guardrails.yaml`), the [sarifix](https://rossedman.io/sarifix/) plugin
(new scanner findings from `sarifix.yaml` before Claude stops) and the
go-bootstrap plugin. Path-scoped standards for Claude live in
`.claude/rules/`. `.guardrails.yaml`, `.golangci.yml` and `sarifix.yaml`
are edited by people only.

## Generated code

The receiver's `internal/metadata` package and `generated_*_test.go`
files come from `metadata.yaml` via the Collector's `mdatagen`. Run
`make generate` after editing `metadata.yaml`; CI checks they are fresh.

## Releases

Releases are cut with [GoReleaser](https://goreleaser.com) from
`.goreleaser.yaml`. They ship `sdmetrics-exporter` for Linux amd64 and
arm64 as archives and as deb and rpm packages that install the unit; the
Go packages are consumed with `go get`.

- **Every branch push** builds a snapshot, versioned like
  `0.1.1-snapshot.my-branch.a1b2c3d`, attached to the workflow run as the
  `sdmetrics-snapshot-<branch>` artifact (kept 14 days).
- **Every `v*` tag** runs the tests and publishes a GitHub release with a
  grouped changelog. Tags with a pre-release suffix (`v0.2.0-rc.1`) are
  marked as pre-releases.
- **The receiver** is its own module: tag it
  `receiver/systemdreportreceiver/vX.Y.Z` after the root tag, and point
  its `require github.com/rossedman/sdmetrics` at that root version.

```sh
make release-check   # validate .goreleaser.yaml
make snapshot        # build dist/ locally, exactly as CI does
git tag -a v0.1.0 -m "v0.1.0" && git push origin v0.1.0
```

Use conventional prefixes in commit subjects (`feat:`, `fix:`, `perf:`) so
the release notes group them.

## Commits and pull requests

- Keep pull requests focused.
- Write commit subjects in the imperative ("scrape: resolve units of
  activated sockets").
- Public API, configuration and mapping changes need a CHANGELOG entry
  and docs.

By contributing you agree that your contributions are licensed under the
MIT license, and to follow the [code of conduct](CODE_OF_CONDUCT.md).
