---
description: How to build, test and contribute to sdmetrics.
---

# Contributing

Thanks for helping! This page is a short summary;
[`CONTRIBUTING.md`](https://github.com/rossedman/sdmetrics/blob/main/CONTRIBUTING.md)
is canonical.

## Quick start

```sh
git clone https://github.com/rossedman/sdmetrics && cd sdmetrics
make test        # unit tests in both modules (no systemd needed)
make race        # with the race detector
make lint        # gofmt, go vet, golangci-lint in both modules
make coverage    # fail below 90% statement coverage in either module
make docs-deps   # pip install -r docs/requirements.txt
make docs-serve  # docs at http://127.0.0.1:8000
```

You need Go 1.26 or newer, on Linux.

## Make targets

| Target | What it does |
| --- | --- |
| `make build` | Build `sdmetrics-exporter` into `./bin`. |
| `make test` | Unit tests in both modules. |
| `make race` | Unit tests with the race detector. |
| `make coverage` | Fail if either module's statement coverage is below 90% (examples excluded). CI runs this. |
| `make cover` | `make coverage`, then a report in `coverage.html`. |
| `make lint` | `gofmt`, `go vet` (including the integration build tag) and `golangci-lint`, in both modules. |
| `make fuzz` | Fuzz the value decoder and the name sanitizer for `FUZZTIME` each (default 30s). |
| `make bench` | Benchmarks: the scraper against 200 fake sources, provider hot paths. |
| `make integration` | Integration tests against the host's systemd (v260+, as root). |
| `make integration-container` | The same in a container booting systemd (docker, cgroup v2). |
| `make tidy` | `go mod tidy` in both modules. |
| `make generate` | Regenerate the receiver's `mdatagen` code. |
| `make check-generated` | Fail if generated code is stale (CI runs this). |
| `make golden` | Rewrite the receiver's golden files after a deliberate change. |
| `make docs-deps` / `make docs-serve` / `make docs` | Install MkDocs, serve the docs, build them with `--strict`. |
| `make release-check` / `make snapshot` | Validate the GoReleaser config / build a local snapshot into `dist/`. |

## Repository layout

| Path | What lives there |
| --- | --- |
| `provider/`, `provider/prombridge/`, `provider/otelbridge/` | The provider SDK and its bridges |
| `scrape/` | The scraper |
| `otelmap/` | The OpenTelemetry mapping |
| `internal/names/` | systemd's naming rules |
| `internal/exporter/`, `cmd/sdmetrics-exporter/` | The exporter |
| `receiver/systemdreportreceiver/` | The Collector receiver (its own module) |
| `packaging/` | The systemd unit, sysusers.d file, default configuration and package scripts |
| `examples/` | Runnable examples, embedded in these docs |
| `integration/` | Tests against real systemd, and the container image CI boots |
| `hack/` | `benchgate` and the integration container script |
| `docs/`, `mkdocs.yml` | This site |

The receiver module uses `replace github.com/rossedman/sdmetrics => ../..`
during development, so changes to `scrape` or `otelmap` are tested by both
modules at once. Run `make tidy` after changing dependencies in either.

## Tests

- Unit tests must not need systemd or root. Serve providers (or a
  `varlinktest` fake) on temporary report directories and scrape them back,
  with a fake `/proc` for unit resolution; see [Testing](../guides/testing.md).
- Coverage stays at 90% or more in each module; CI fails below that.
- After a deliberate mapping change, `make golden` rewrites the receiver's
  golden files in `receiver/systemdreportreceiver/testdata/scraper/`; review
  the diff.
- Benchmarks are compared against the base branch on every pull request;
  a median regression over 25% fails the check.
- Integration tests clean up the transient units they create and change
  nothing else on the host.

## Working on the docs

```sh
make docs-deps    # once
make docs-serve   # live reload at http://127.0.0.1:8000
make docs         # what CI runs: mkdocs build --strict
```

- Pages are Markdown under `docs/`, rendered with
  [Material for MkDocs](https://squidfunk.github.io/mkdocs-material/).
- Examples are embedded from `examples/` with snippets
  (`--8<-- "examples/<name>/main.go"`), so they can't drift from the code.
- Every Go block should compile against the current API.
- Diagrams are [Mermaid](https://mermaid.js.org/) fences.

## Commits and pull requests

- Keep pull requests focused; put generated changes in their own commit.
- Use conventional prefixes, so the release notes group them: `feat:`,
  `fix:`, `perf:`; `docs:`, `test:`, `ci:` and `chore:` are left out of the
  notes.
- Public API and configuration changes need a CHANGELOG entry and docs.

By contributing you agree that your contributions are licensed under the MIT
license, and to follow the
[code of conduct](https://github.com/rossedman/sdmetrics/blob/main/CODE_OF_CONDUCT.md).
