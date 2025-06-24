.DEFAULT_GOAL := build
.PHONY: help test build deploy run clean

GO_FLAGS   ?=
NAME       := fan2go
OUTPUT_BIN ?= bin/${NAME}
PACKAGE    := github.com/markusressel/$(NAME)
GIT_REV    ?= $(shell git rev-parse --short HEAD)
SOURCE_DATE_EPOCH ?= $(shell date +%s)
DATE       ?= $(shell date -u -d @${SOURCE_DATE_EPOCH} +"%Y-%m-%dT%H:%M:%SZ")
VERSION    ?= 0.11.0

test:   ## Run all tests
	@go clean --testcache && go test -tags disable_nvml -v ./...

build:  ## Builds the CLI
	@go build ${GO_FLAGS} \
	-ldflags "-w -s \
	-extldflags=-Wl,-z,lazy \
	-X ${NAME}/cmd/global.Version=${VERSION} \
	-X ${PACKAGE}/cmd/global.Version=${VERSION} \
	-X ${NAME}/cmd/global.Commit=${GIT_REV} \
	-X ${PACKAGE}/cmd/global.Commit=${GIT_REV} \
	-X ${NAME}/cmd/global.Date=${DATE} \
	-X ${PACKAGE}/cmd/global.Date=${DATE}" \
	-a -tags netgo -o "${OUTPUT_BIN}" main.go

build-no-nvml: ## Builds the CLI without nvml (nvidia GPU) support
	@go build ${GO_FLAGS} \
	-ldflags "-w -s \
	-X ${NAME}/cmd/global.Version=${VERSION} \
	-X ${PACKAGE}/cmd/global.Version=${VERSION} \
	-X ${NAME}/cmd/global.Commit=${GIT_REV} \
	-X ${PACKAGE}/cmd/global.Commit=${GIT_REV} \
	-X ${NAME}/cmd/global.Date=${DATE} \
	-X ${PACKAGE}/cmd/global.Date=${DATE}" \
	-a -tags netgo,disable_nvml -o "${OUTPUT_BIN}" main.go

run: build
	./${OUTPUT_BIN}

deploy: build
	sudo cp "${OUTPUT_BIN}" "/usr/bin/${NAME}"

clean:
	go clean
	rm -f "${OUTPUT_BIN}"
