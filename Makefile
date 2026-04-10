VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BINARY_NAME = lark-agent-bridge
BUILD_DIR = build

LDFLAGS = -ldflags "-s -w -X main.version=$(VERSION)"

.PHONY: all build linux windows cross deb clean tidy

all: build

build:
	go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/bridge

linux:
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)_linux_amd64 ./cmd/bridge

windows:
	GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)_windows_amd64.exe ./cmd/bridge

cross: linux windows

# .deb 包构建
deb: linux
	$(eval DEB_DIR := $(BUILD_DIR)/deb)
	$(eval DEB_ARCH := amd64)
	$(eval DEB_VERSION := $(shell echo "$(VERSION)" | sed 's/^v//; s/-dirty//; s/-/./g'))
	rm -rf $(DEB_DIR)
	mkdir -p $(DEB_DIR)/DEBIAN
	mkdir -p $(DEB_DIR)/usr/local/bin
	mkdir -p $(DEB_DIR)/usr/share/$(BINARY_NAME)
	# 控制文件
	sed 's/{{VERSION}}/$(DEB_VERSION)/g; s/{{ARCH}}/$(DEB_ARCH)/g' \
		installer/linux/debian/control > $(DEB_DIR)/DEBIAN/control
	cp installer/linux/debian/postinst $(DEB_DIR)/DEBIAN/postinst
	cp installer/linux/debian/prerm $(DEB_DIR)/DEBIAN/prerm
	chmod 755 $(DEB_DIR)/DEBIAN/postinst $(DEB_DIR)/DEBIAN/prerm
	# 二进制
	cp $(BUILD_DIR)/$(BINARY_NAME)_linux_amd64 $(DEB_DIR)/usr/local/bin/$(BINARY_NAME)
	chmod 755 $(DEB_DIR)/usr/local/bin/$(BINARY_NAME)
	# 配置模板
	cp config.example.yaml $(DEB_DIR)/usr/share/$(BINARY_NAME)/config.example.yaml
	# 构建 deb
	dpkg-deb --build $(DEB_DIR) $(BUILD_DIR)/$(BINARY_NAME)_$(DEB_VERSION)_$(DEB_ARCH).deb
	@echo "构建完成: $(BUILD_DIR)/$(BINARY_NAME)_$(DEB_VERSION)_$(DEB_ARCH).deb"

clean:
	rm -rf $(BUILD_DIR)

tidy:
	go mod tidy
