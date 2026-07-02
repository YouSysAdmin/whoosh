MODULE   := github.com/yousysadmin/whoosh
VERSION  ?= dev
LDFLAGS  := -s -w -X $(MODULE)/internal/version.Version=$(VERSION)
GOFLAGS  := -trimpath
TAGS     ?=
GO_TAGS  := $(if $(TAGS),-tags '$(TAGS)',)
DIST_DIR  := dist
BIN_NAME := whoosh
BIN  := $(DIST_DIR)/$(BIN_NAME)

# Separate go modules in this repo: `go test ./...` stops at go.mod boundaries, so each needs its own run.
PLUGIN_MODULES := plugins/aws plugins/rbenv

.PHONY: all build build-aws build-minimal run test test-v vet fmt lint schema clean help

## all: Show help (default)
all: help

## build: Build the whoosh binary - no AWS plugins (pass TAGS="noplugins" to drop the bundled plugins)
build:
	go build $(GOFLAGS) $(GO_TAGS) -ldflags '$(LDFLAGS)' -o $(BIN) ./cmd/whoosh

## build-aws: Build a binary WITH the AWS plugins, via `whoosh build` against the local checkout
## (releases also ship whoosh-aws_* archives, built by goreleaser from the cmd/whoosh-aws module)
build-aws:
	go run ./cmd/whoosh build \
	  --replace github.com/yousysadmin/whoosh=$(CURDIR) \
	  --with github.com/yousysadmin/whoosh/plugins/aws \
	  --replace github.com/yousysadmin/whoosh/plugins/aws=$(CURDIR)/plugins/aws \
	  -o $(CURDIR)/$(BIN)

## build-minimal: Build without any plugins (smaller still)
build-minimal:
	go build $(GOFLAGS) -tags noplugins -ldflags '$(LDFLAGS)' -o $(BIN)-minimal ./cmd/whoosh


## run: Run in CLI mode (pass ARGS="..." for extra flags)
run: build
	$(BIN) $(ARGS)

## test: Run all tests (root + plugin modules; also proves cmd/whoosh-aws compiles)
test:
	go test ./...
	@for m in $(PLUGIN_MODULES); do echo "== $$m"; (cd $$m && go test ./...) || exit 1; done
	@echo "== cmd/whoosh-aws (build only)"
	@cd cmd/whoosh-aws && go build -o /dev/null .

## test-v: Run all tests with verbose output (root + plugin modules)
test-v:
	go test -v ./...
	@for m in $(PLUGIN_MODULES); do echo "== $$m"; (cd $$m && go test -v ./...) || exit 1; done

## vet: Run go vet (root + plugin modules)
vet:
	go vet ./...
	@for m in $(PLUGIN_MODULES); do echo "== $$m"; (cd $$m && go vet ./...) || exit 1; done

## fmt: Run gofmt on all Go files
fmt:
	gofmt -w .

## lint: Run vet and check formatting
lint: vet
	@test -z "$$(gofmt -l .)" || (echo "Files need formatting:" && gofmt -l . && exit 1)

## schema: Generate the Deployfile JSON Schema (deployfile.schema.json) for editor validation
schema:
	go run ./cmd/gen-schema -o deployfile.schema.json

## clean: Remove built binaries
clean:
	rm -rf $(DIST_DIR)

## help: Show this help message
help:
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/^## /  /'
