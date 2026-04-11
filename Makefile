GIT_VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME ?= $(shell date '+%Y%m%d-%H%M%S')
VERSION ?= $(if $(findstring dirty,$(GIT_VERSION)),$(GIT_VERSION)-$(BUILD_TIME),$(GIT_VERSION))
BINARY_NAME = lark-agent-bridge
BUILD_DIR = build

LDFLAGS = -ldflags "-s -w -X main.version=$(VERSION)"

.PHONY: all build linux windows cross clean tidy

all: build

build:
	go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/bridge

linux:
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)_linux_amd64 ./cmd/bridge

windows:
	GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)_windows_amd64.exe ./cmd/bridge

cross: linux windows

clean:
	rm -rf $(BUILD_DIR)

tidy:
	go mod tidy
