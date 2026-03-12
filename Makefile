GO ?= go
BIN_DIR ?= bin
PREFIX ?= $(HOME)/.local
INSTALL_BIN_DIR ?= $(PREFIX)/bin

.PHONY: all build build-roma build-romad build-romatui test install clean

all: build

build: build-roma build-romad build-romatui

$(BIN_DIR):
	mkdir -p $(BIN_DIR)

build-roma: | $(BIN_DIR)
	$(GO) build -o $(BIN_DIR)/roma ./cmd/roma

build-romad: | $(BIN_DIR)
	$(GO) build -o $(BIN_DIR)/romad ./cmd/romad

build-romatui: | $(BIN_DIR)
	$(GO) build -o $(BIN_DIR)/romatui ./cmd/romatui

test:
	$(GO) test -count=1 ./...

install: build
	mkdir -p $(INSTALL_BIN_DIR)
	install -m 0755 $(BIN_DIR)/roma $(INSTALL_BIN_DIR)/roma
	install -m 0755 $(BIN_DIR)/romad $(INSTALL_BIN_DIR)/romad
	install -m 0755 $(BIN_DIR)/romatui $(INSTALL_BIN_DIR)/romatui

clean:
	rm -rf $(BIN_DIR)
