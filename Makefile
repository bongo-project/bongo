.PHONY: all build generate test test-race lint sec clean run

BINARY     := bongo-go
IMPORT_BIN := bongo-import
BUILD_DIR  := bin

all: generate build

## Build targets

build: generate
	@mkdir -p $(BUILD_DIR)
	go build -o $(BUILD_DIR)/$(BINARY) ./cmd/bongo-go
	go build -o $(BUILD_DIR)/$(IMPORT_BIN) ./cmd/bongo-import

generate:
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
	gosec ./...

## Run targets

run: build
	$(BUILD_DIR)/$(BINARY) serve --dev

## Clean

clean:
	rm -rf $(BUILD_DIR)
