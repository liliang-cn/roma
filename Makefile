GO ?= go
GO_ENV ?= GOWORK=off
BIN_DIR ?= bin
PREFIX ?= $(HOME)/.local
INSTALL_BIN_DIR ?= $(PREFIX)/bin

.PHONY: all build build-roma build-romad build-romatui desktop-frontend-build desktop-build test install clean

WAILS ?= $(shell $(GO) env GOPATH)/bin/wails
DESKTOP_WAILS_TAGS ?= webkit2_41

all: build

build: build-roma build-romad build-romatui

$(BIN_DIR):
	mkdir -p $(BIN_DIR)

build-roma: | $(BIN_DIR)
	$(GO_ENV) $(GO) build -o $(BIN_DIR)/roma ./cmd/roma

build-romad: | $(BIN_DIR)
	$(GO_ENV) $(GO) build -o $(BIN_DIR)/romad ./cmd/romad

build-romatui: | $(BIN_DIR)
	$(GO_ENV) $(GO) build -o $(BIN_DIR)/romatui ./cmd/romatui

desktop-frontend-build:
	cd desktop/frontend && npm install && npm run build

desktop-build: desktop-frontend-build
	cd desktop && GOWORK=off $(WAILS) build -nopackage -m -s -tags "$(DESKTOP_WAILS_TAGS)"

test:
	$(GO_ENV) $(GO) test -count=1 ./...

install: build
	mkdir -p $(INSTALL_BIN_DIR)
	install -m 0755 $(BIN_DIR)/roma $(INSTALL_BIN_DIR)/roma
	install -m 0755 $(BIN_DIR)/romad $(INSTALL_BIN_DIR)/romad
	install -m 0755 $(BIN_DIR)/romatui $(INSTALL_BIN_DIR)/romatui

clean:
	rm -rf $(BIN_DIR)
