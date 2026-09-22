# HowlInstinct development tasks.
#
# Every target here runs offline. Nothing in the default path needs network
# access, credentials, or inference hardware.

GO      ?= go
BINARY  ?= build/howlinstinct
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.0.0-dev)
LDFLAGS := -s -w -X github.com/howlcipher/howlinstinct/internal/version.Version=$(VERSION)

.PHONY: help build install test test-race test-coverage lint fmt vet check evals demo clean tidy

help:
	@echo "build         build the howlinstinct binary into $(BINARY)"
	@echo "test          run the full test suite (offline, no credentials)"
	@echo "test-race     run the suite under the race detector"
	@echo "test-coverage run the suite and report coverage"
	@echo "lint          gofmt check, go vet, and golangci-lint"
	@echo "check         everything CI runs, in the same order"
	@echo "evals         run every shipped evaluation suite against the mock"
	@echo "demo          run the dogfood example"

build:
	$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(BINARY) ./cmd/howlinstinct

install:
	$(GO) install -trimpath -ldflags '$(LDFLAGS)' ./cmd/howlinstinct

test:
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

test-coverage:
	$(GO) test -coverprofile=coverage.out -covermode=atomic ./...
	$(GO) tool cover -func=coverage.out | tail -1

fmt:
	gofmt -w .

vet:
	$(GO) vet ./...

# gofmt -l prints files needing formatting and exits 0 either way, so the
# output itself has to be turned into the failure.
lint: vet
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "these files are not gofmt clean:"; echo "$$unformatted"; exit 1; \
	fi
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run; \
	else \
		echo "golangci-lint not installed locally, skipping (CI runs it)"; \
	fi

check: lint test-race

evals: build
	@for suite in evals/*/cases.yaml; do \
		echo "=== $$suite"; \
		$(BINARY) eval --dataset "$$suite" || exit 1; \
	done

demo:
	$(GO) run ./examples/dogfood

tidy:
	$(GO) mod tidy

clean:
	rm -rf build coverage.out
