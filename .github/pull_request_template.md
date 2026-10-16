## What and why

<!-- What does this change, and why? Link issues with "Fixes #123". -->

## Checklist

- [ ] `make test lint coverage` passes (coverage stays ≥ 90%)
- [ ] Generated code is fresh (`make check-generated`) and modules are tidy (`make tidy`)
- [ ] Public API, config or metric-mapping changes are documented and listed in CHANGELOG.md
- [ ] New behavior has tests (a fake report directory for the scraper, `varlinktest` or a provider for anything that talks Varlink)
