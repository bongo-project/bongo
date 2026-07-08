.PHONY: all build generate test test-race lint sec clean run dev ui-test ui-test-headed ui-test-setup validate seed

BINARY     := bongo-go
IMPORT_BIN := bongo-import
BUILD_DIR  := bin
SEC_GOCACHE ?= /tmp/bongo-gocache-security
VERSION_PKG := github.com/bongo-go/bongo-go/internal/version
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

generate:
	@touch internal/web/templates/*.templ
	templ generate

## Test targets

test:
	go test ./...

test-race:
	go test -race ./...

test-cover:
	go test -race -cover ./...

## Quality targets

lint:
	golangci-lint run

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
	go run ./scripts/validate-server/...
	go run ./scripts/validate-server/... --seed-only

validate:
	go run ./scripts/validate-server/...

seed:
	go run ./scripts/validate-server/... --seed-only

## Clean

clean:
	rm -rf $(BUILD_DIR)
