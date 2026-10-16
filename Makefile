# sdmetrics developer tasks. Run `make help` for a list.

GO        ?= go
RECEIVER  := receiver/systemdreportreceiver
FUZZTIME  ?= 30s
BENCHTIME ?= 1s
MDATAGEN  ?= mdatagen

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z_-]+:.*##/ {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.PHONY: build
build: ## Build sdmetrics-exporter into ./bin
	CGO_ENABLED=0 $(GO) build -trimpath -o bin/ ./cmd/sdmetrics-exporter

.PHONY: test
test: ## Run unit tests in both modules
	$(GO) test ./...
	cd $(RECEIVER) && $(GO) test ./...

.PHONY: race
race: ## Run unit tests with the race detector
	$(GO) test -race -count=1 ./...
	cd $(RECEIVER) && $(GO) test -race -count=1 ./...

.PHONY: cover
cover: coverage ## Write a coverage report to coverage.html
	$(GO) tool cover -html=coverage.out -o coverage.html

.PHONY: coverage
coverage: ## Fail if either module's coverage is below 90% (examples excluded)
	hack/coverage.sh

.PHONY: integration
integration: ## Run integration tests against the host's systemd (v260+, as root)
	CGO_ENABLED=0 $(GO) build -o bin/queued ./examples/queued
	SDMETRICS_QUEUED=$(CURDIR)/bin/queued $(GO) test -tags integration -count=1 -v ./integration/

.PHONY: integration-container
integration-container: ## Run integration tests in a container booting systemd (needs docker, cgroup v2)
	hack/integration-container.sh

.PHONY: fuzz
fuzz: ## Fuzz the value decoder and name sanitizer for FUZZTIME each
	$(GO) test ./scrape/ -run '^$$' -fuzz FuzzDecodeValue -fuzztime $(FUZZTIME)
	$(GO) test ./internal/names/ -run '^$$' -fuzz FuzzSanitizeField -fuzztime $(FUZZTIME)

.PHONY: bench
bench: ## Run benchmarks (scraper against 200 fake sources, provider hot paths)
	$(GO) test -run '^$$' -bench . -benchmem -benchtime $(BENCHTIME) ./scrape/ ./provider/

.PHONY: lint
lint: ## Run gofmt, go vet and golangci-lint in both modules
	@test -z "$$(gofmt -l . | grep -v '^site/')" || (gofmt -l .; exit 1)
	$(GO) vet ./...
	$(GO) vet -tags integration ./integration/
	golangci-lint run ./...
	cd $(RECEIVER) && $(GO) vet ./... && golangci-lint run --config ../../.golangci.yml ./...

.PHONY: tidy
tidy: ## Tidy both modules
	$(GO) mod tidy
	cd $(RECEIVER) && $(GO) mod tidy

.PHONY: generate
generate: ## Regenerate the receiver's mdatagen code
	cd $(RECEIVER) && $(MDATAGEN) metadata.yaml

.PHONY: check-generated
check-generated: generate ## Fail if generated code is stale
	@git diff --exit-code -- $(RECEIVER) || \
		(echo "generated files are stale: run 'make generate'"; exit 1)

.PHONY: golden
golden: ## Rewrite the receiver's golden files after a deliberate change
	cd $(RECEIVER) && UPDATE_GOLDEN=1 $(GO) test -run TestScrapeGolden ./... || true
	cd $(RECEIVER) && $(GO) test -run TestScrapeGolden ./...

.PHONY: docs-deps
docs-deps: ## Install MkDocs and plugins
	pip install -r docs/requirements.txt

.PHONY: docs-serve
docs-serve: ## Serve the docs at http://127.0.0.1:8000
	mkdocs serve

.PHONY: docs
docs: ## Build the docs site into site/
	mkdocs build --strict

.PHONY: release-check
release-check: ## Validate the GoReleaser config
	goreleaser check

.PHONY: snapshot
snapshot: ## Build a local GoReleaser snapshot into dist/
	goreleaser release --snapshot --clean
