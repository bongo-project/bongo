.PHONY: all build dist generate test test-race test-integration test-all fmt fmt-check lint check-image-pins sec clean run dev ui-test ui-test-headed ui-test-setup ui-shots validate seed

BINARY     := bongo-go
IMPORT_BIN := bongo-import
BUILD_DIR  := bin
SEC_GOCACHE ?= /tmp/bongo-gocache-security
VERSION_PKG := github.com/bongo-project/bongo/internal/version
BUILD_VERSION ?= $(shell git describe --tags --exact-match 2>/dev/null || echo dev)
BUILD_COMMIT ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
BUILD_LDFLAGS := -X $(VERSION_PKG).Version=$(BUILD_VERSION) -X $(VERSION_PKG).Commit=$(BUILD_COMMIT) -X $(VERSION_PKG).Date=$(BUILD_DATE)

all: generate build

## Build targets

build: generate
	@mkdir -p $(BUILD_DIR)
	go build -ldflags "$(BUILD_LDFLAGS)" -o $(BUILD_DIR)/$(BINARY) ./cmd/bongo-go
	go build -ldflags "$(BUILD_LDFLAGS)" -o $(BUILD_DIR)/$(IMPORT_BIN) ./cmd/bongo-import

## dist builds shareable standalone binary release zips (linux amd64 + arm64)
## under dist/. Override targets with: make dist PLATFORMS="linux/amd64".
dist:
	scripts/package-release.sh

generate:
	@touch internal/web/templates/*.templ
	templ generate

## Test targets

## NOTE: the integration suite is behind `//go:build integration`, so a plain
## `go test ./...` SKIPS IT ENTIRELY AND SAYS NOTHING. That is how a broken
## integration suite stayed red in CI for a month while local runs looked green.
## `test` remains the fast inner-loop target; use `test-all` before pushing.

test:
	go test ./...

## An explicit -timeout rather than Go's 10 minute default: internal/web/handlers
## alone takes minutes under -race, and a loaded machine or a slower CI runner
## multiplies that. 20m leaves headroom without hiding a genuine hang.
test-race:
	go test -race -timeout 20m ./...

test-cover:
	go test -race -cover -timeout 20m ./...

## The integration suite only. Slower (~6 min) and starts real servers.
test-integration:
	go test -race -tags=integration -timeout 800s ./test/integration/...

## Everything CI runs, in CI's order. Use this before pushing.
test-all: fmt-check test-race test-integration
	@echo "test-all: unit + integration passed, formatting clean"

## Quality targets

fmt:
	gofmt -w $(shell gofmt -l . | grep -v '^vendor/')

## Fails when any file is unformatted. golangci-lint does not check gofmt here,
## so without this the formatting drifts silently.
fmt-check:
	@unformatted=$$(gofmt -l . | grep -v '^vendor/' || true); \
	if [ -n "$$unformatted" ]; then \
		echo "These files are not gofmt-clean:"; \
		echo "$$unformatted"; \
		echo "Run: make fmt"; \
		exit 1; \
	fi
	@echo "fmt-check: all files gofmt-clean"

lint:
	golangci-lint run

check-image-pins:
	go run ./scripts/checkimagepins deploy/docker-compose.yml

sec:
	GOCACHE=$(SEC_GOCACHE) govulncheck ./...
	GOCACHE=$(SEC_GOCACHE) gosec -quiet -exclude-generated -severity=medium ./...

## Run targets

run: build
	$(BUILD_DIR)/$(BINARY) serve --dev

dev: build
	@echo "Starting dev server..."
	@$(BUILD_DIR)/$(BINARY) serve --dev --plain-http & \
	SERVER_PID=$$!; \
	trap "kill $$SERVER_PID 2>/dev/null; exit" INT TERM EXIT; \
	sleep 3; \
	echo "Running validate-server..."; \
	go run ./scripts/validate-server/... --plain-http; \
	echo "Seeding test data..."; \
	go run ./scripts/validate-server/... --plain-http --seed-only; \
	echo "Dev server ready at http://localhost:8080 (PID $$SERVER_PID)"; \
	wait $$SERVER_PID

## UI test targets

ui-test:
	go run ./scripts/ui-test/...

ui-test-headed:
	go run ./scripts/ui-test/... --headed --slow 300

ui-test-setup: build
	$(BUILD_DIR)/$(BINARY) serve --dev &
	sleep 3
	go run ./scripts/validate-server/... --plain-http
	go run ./scripts/validate-server/... --plain-http --seed-only

# Visual verification harness: builds into a temp dir, starts a dev server on
# free ports, seeds it, and screenshots every route (light, dark, mobile).
# Not part of test-all or CI. OUT must be outside the repository.
#   make ui-shots OUT=~/shots
# Any failure (UI findings or a setup error) surfaces as make's exit status 2
# (go run collapses the program's 1 and 2 to 1); the program's own 0/1/2 codes
# are only visible via go run ./scripts/ui-shots.
ui-shots:
	go run ./scripts/ui-shots $(if $(OUT),-out $(OUT))

validate:
	go run ./scripts/validate-server/... --plain-http

seed:
	go run ./scripts/validate-server/... --plain-http --seed-only

## Clean

clean:
	rm -rf $(BUILD_DIR)
