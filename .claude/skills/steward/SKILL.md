---
name: steward
description: Drive an sdmetrics pull request to green and mergeable - reproduce each failing CI job locally, apply the repo's fix for it, handle Dependabot and sarifix PRs, and know what needs a person. Use when babysitting, fixing CI on, or reviewing-to-merge any sdmetrics pull request, including ones Claude opened.
---

# Steward sdmetrics pull requests

sdmetrics is two Go modules: the root and `receiver/systemdreportreceiver`.
The receiver depends on the root through a development `replace`. Most CI
jobs run against both modules, and so should every local check.

## Before every push

Run these from the repo root. All of them must pass before you push:

```sh
make test lint coverage doccheck   # both modules; coverage ≥ 90% each
make tidy && git diff --exit-code -- go.mod go.sum receiver/systemdreportreceiver/go.mod receiver/systemdreportreceiver/go.sum
make fuzz FUZZTIME=10s             # when scrape/ or internal/names/ changed
```

Also run what matches the change:

| Changed | Also run |
| --- | --- |
| `receiver/**/metadata.yaml` or the collector version | `make check-generated` (needs mdatagen; see the `generated` job in `ci.yml` for how CI builds it) |
| `otelmap/`, `scrape/` or the receiver's scraper | the receiver tests; if goldens differ, see "Golden files" |
| `docs/`, `mkdocs.yml`, `README.md`, `CHANGELOG.md`, `CONTRIBUTING.md` | `make docs-deps docs` (a strict build) |
| `.goreleaser.yaml` or `release.yml` | `make release-check snapshot` |
| `.github/workflows/` | `actionlint`. Pin every new action to a full commit SHA with a `# vX.Y.Z` comment, resolved with `git ls-remote`, never from memory. |
| Go code | `guardrails gate --base origin/main`, the same gate the plugin runs at Stop |

If `go test -cover` fails with `no such tool "covdata"`, set
`GOTOOLCHAIN` to the newest installed 1.26.x toolchain.

## CI jobs and how to fix them

| Job (workflow) | Reproduce | Usual cause and fix |
| --- | --- | --- |
| Test, Go stable/oldstable × amd64/arm64 (`ci.yml`) | `make race` | Data race or a test that assumes amd64. Fix the code; never add `t.Skip`. |
| Coverage ≥ 90% (`ci.yml`) | `hack/coverage.sh` | New code without tests. The failure lists the least-covered functions. Add table-driven tests; don't lower `MIN`. |
| Lint (`ci.yml`) | `make lint` | Fix the finding. Suppressions and `.golangci.yml` edits are for people only. |
| Doc comments and govulncheck (`ci.yml`) | `make doccheck vuln` | Missing doc comment: add one that starts with the name. Vulnerability: bump the module in both `go.mod` files, then `make tidy`. |
| Fuzz smoke (`ci.yml`), Fuzz (`fuzz.yml`, nightly) | `make fuzz FUZZTIME=30s` | A crasher. Commit the input under `testdata/fuzz/<Target>/` and fix the code so it passes. |
| Generated code is fresh (`ci.yml`) | `make check-generated tidy` | Stale mdatagen output or untidy modules. Regenerate; never hand-edit `generated_*` or `internal/metadata`. |
| Benchmarks vs base (`ci.yml`, PRs only) | `make bench` on both branches, compared with `benchstat` | Median slowdown over 25%. Find the cause in the changed hot path (see "Critical paths" in `CONTRIBUTING.md`). Shared runners are noisy: when the PR touches no benchmarked package (`scrape`, `provider`), it's a noise candidate for the one allowed re-run. |
| Build linux/amd64, arm64 (`ci.yml`) | `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./...` in both modules | Linux-only code missing its `_other.go` counterpart, or the other way round. |
| systemd (fedora:rawhide / archlinux:latest) (`integration.yml`) | `make integration-container` (docker, cgroup v2) | Rawhide and Arch move daily. If the same job also fails on main's nightly scheduled run, it isn't this PR's failure: say so once on the PR and don't patch around it here. Otherwise fix the code or the test. |
| Docs (`docs.yml`) | `make docs` | Broken link, missing nav entry or invalid admonition. A strict build fails on warnings. |
| Release snapshot (`release.yml`) | `make snapshot` | Invalid `.goreleaser.yaml` or a packaging file that moved. |
| sarifix (`sarifix.yml`) | `sarifix check` with the scanners installed | A finding the PR introduced. Fix the code. `sarifix.yaml`, `.trivyignore.yaml` entries and `rules.deny` are for people to change; propose the change instead. |

## Golden files

Receiver goldens live in `receiver/systemdreportreceiver/testdata/scraper/`.
Run `make golden` only when the PR deliberately changes the metric mapping.
Then review the diff, add a CHANGELOG entry under `[Unreleased]` and update
the mapping docs. If the mapping change wasn't intended, fix the code, not
the goldens.

## Dependabot pull requests

- The `opentelemetry` group bumps the root module. The `collector` group
  bumps the receiver. A root bump often needs the receiver's `go.mod`
  updated to match: run `make tidy`, commit both modules, push.
- Collector bumps can change mdatagen's output: run `make check-generated`
  and commit the regenerated files.
- Action bumps from Dependabot keep the SHA-plus-comment pin. Check that
  the comment names the new version.
- A bump that breaks the build or API needs a real code change. Make it
  if it's small and local; otherwise describe it on the PR.

## Conventions

- Commit subjects use Conventional Commit types (`feat:`, `fix:`, `perf:`,
  `docs:`, `test:`, `ci:`, `build:`, `chore:`), optionally scoped by
  package (`fix(scrape): …`). Release notes group by these types.
- Bring `main` into a PR branch with a merge, not a rebase, unless Claude
  created the branch in this session.
- Public API, configuration and metric-mapping changes need a CHANGELOG
  entry and docs in the same PR.
- `.guardrails.yaml`, `.claude/rules/`, `.golangci.yml`, `sarifix.yaml`
  and `.claude/settings.json` belong to people. Propose changes as a diff
  in a comment.

## How proactive to be

- **Just do it:** CI fixes in code the PR touches; review nits and
  renames; added tests; Dependabot tidy and regeneration; doc comments;
  CHANGELOG entries the PR is missing. Plainly correct optional review
  nits ride along with the next push that already changes the PR's files.
- **Ask first:** changing an exported API or a metric name, attribute or
  unit; adding a dependency; lowering any gate (coverage, the benchmark
  threshold, lint rules, scanner rules); changing the release or packaging
  setup; anything touching the security model in `SECURITY.md`.
- **Hand to a person:** a leaked-secret finding from gitleaks (it needs
  rotation, not an edit), and integration failures that reproduce only on
  real hardware or a specific systemd version.
