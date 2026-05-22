.DEFAULT_GOAL := build
.PHONY: help test build build-no-nvml build-cross build-cross-no-nvml deploy run clean

GO_FLAGS   ?=
NAME       := fan2go
OUTPUT_BIN ?= bin/${NAME}
PACKAGE    := github.com/markusressel/$(NAME)
GIT_REV    ?= $(shell git rev-parse --short HEAD)
SOURCE_DATE_EPOCH ?= $(shell date +%s)
DATE       ?= $(shell date -u -d @${SOURCE_DATE_EPOCH} +"%Y-%m-%dT%H:%M:%SZ")
VERSION    ?= 0.14.0

# Shared linker flags for all targets
LDFLAGS := -w -s \
	-X ${NAME}/cmd/global.Version=${VERSION} \
	-X ${PACKAGE}/cmd/global.Version=${VERSION} \
	-X ${NAME}/cmd/global.Commit=${GIT_REV} \
	-X ${PACKAGE}/cmd/global.Commit=${GIT_REV} \
	-X ${NAME}/cmd/global.Date=${DATE} \
	-X ${PACKAGE}/cmd/global.Date=${DATE}

test:   ## Run all tests
	@go clean --testcache && go test -tags disable_nvml -v ./...

build:  ## Builds the CLI for the native architecture
	@go build ${GO_FLAGS} \
	-ldflags "${LDFLAGS} -extldflags=-Wl,-z,lazy" \
	-a -tags netgo -o "${OUTPUT_BIN}" main.go

build-no-nvml: ## Builds the CLI without nvml (nvidia GPU) support for the native architecture
	@go build ${GO_FLAGS} \
	-ldflags "${LDFLAGS}" \
	-a -tags netgo,disable_nvml -o "${OUTPUT_BIN}" main.go

# Cross-compilation targets: set CC, GOOS, GOARCH, and OUTPUT_BIN via environment or make variables.
# Example: make build-cross CC=powerpc64le-linux-gnu-gcc GOOS=linux GOARCH=ppc64le OUTPUT_BIN=dist/fan2go-linux-ppc64le
build-cross: ## Builds the CLI for a foreign architecture via CGO cross-compilation
	CGO_ENABLED=1 go build ${GO_FLAGS} \
	-ldflags "${LDFLAGS}" \
	-a -tags netgo -o "${OUTPUT_BIN}" main.go

build-cross-no-nvml: ## Builds the CLI without nvml for a foreign architecture via CGO cross-compilation
	CGO_ENABLED=1 go build ${GO_FLAGS} \
	-ldflags "${LDFLAGS}" \
	-a -tags netgo,disable_nvml -o "${OUTPUT_BIN}" main.go

run: build
	./${OUTPUT_BIN}

deploy: build
	sudo cp "${OUTPUT_BIN}" "/usr/bin/${NAME}"

clean:
	go clean
	rm -f "${OUTPUT_BIN}"
