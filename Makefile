# comfylib is a library: these targets only check it.
GOCYCLO ?= go run github.com/fzipp/gocyclo/cmd/gocyclo@v0.6.0
GOLANGCI_LINT ?= golangci-lint
FUZZTIME ?= 10s

# A go.work left in a worktree must never change what is tested.
export GOWORK := off

.DEFAULT_GOAL := help
.PHONY: help check fmt vet cyclo lint test test-race test-sandbox test-proxies test-mutations test-engine fuzz-smoke

help: ## Show available commands
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z0-9_-]+:.*## / {printf "  %-16s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

check: fmt vet cyclo lint test-race test-engine test-mutations ## Run every check that needs no extra services
	@echo 'All checks passed.'

fmt: ## Fail if any Go file needs gofmt
	@unformatted=$$(gofmt -l .) || exit 1; \
		if [ -n "$$unformatted" ]; then echo "Run gofmt on: $$unformatted"; exit 1; fi

vet: ## Run go vet, including the proxy integration test
	go vet ./...
	go vet -tags=proxyintegration ./...

cyclo: ## Fail on functions with cyclomatic complexity over 15
	$(GOCYCLO) -over 15 .

lint: ## Run golangci-lint
	$(GOLANGCI_LINT) run --build-tags=proxyintegration ./...

test: ## Run Go tests
	go test -count=1 ./...

test-race: ## Run Go tests with the race detector
	go test -race -count=1 ./...

test-sandbox: ## Run tests that need a working bwrap
	COMFYWARE_SANDBOX_TEST=1 go test -race -count=1 ./...

test-proxies: ## Test generated nginx and Apache configurations in real servers
	go test -tags=proxyintegration -count=1 -timeout=60s ./proxyconfig/proxytest

test-mutations: ## Check that regression tests reject every mutation in mutations/
	python3 tools/mutate.py mutations/*.json

test-engine: ## Self-test the mutation engine
	python3 tools/test_mutate.py

fuzz-smoke: ## Run every fuzz target for FUZZTIME (default 10s)
	sh tools/fuzz.sh "$(FUZZTIME)"
