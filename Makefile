MODULE  := github.com/nlink-jp/video-studio-mcp
BINARY  := video-studio-mcp
BIN_DIR := dist

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-s -w -X $(MODULE)/cmd.Version=$(VERSION)"

# macOS Developer ID signing / notarization (see CONVENTIONS.md §Code
# Signing). Defaults match any Developer ID Application cert in the keychain
# and the org-standard notary profile. Builds without these fall back to
# ad-hoc / un-notarized with a one-line warning.
CODESIGN_IDENTITY ?= Developer ID Application
NOTARY_PROFILE    ?= nlink-jp-notary

# ffmpeg is a runtime dependency on every platform, so unlike voice-studio-mcp
# this server ships all five org-standard targets.
PLATFORMS := \
	linux/amd64 \
	linux/arm64 \
	darwin/amd64 \
	darwin/arm64 \
	windows/amd64

.PHONY: build build-all package test clean help

## build: Build binary for the current OS/Arch → ./dist/video-studio-mcp
build:
	@mkdir -p $(BIN_DIR)
	go build $(LDFLAGS) -o $(BIN_DIR)/$(BINARY) .
	@scripts/codesign-darwin.sh $(BIN_DIR)/$(BINARY) "$(CODESIGN_IDENTITY)"

## build-all: Cross-compile every release platform (codesign darwin builds)
build-all:
	@mkdir -p $(BIN_DIR)
	$(foreach platform,$(PLATFORMS),$(call build_platform,$(platform)))

define build_platform
	$(eval OS   := $(word 1,$(subst /, ,$(1))))
	$(eval ARCH := $(word 2,$(subst /, ,$(1))))
	$(eval EXT  := $(if $(filter windows,$(OS)),.exe,))
	$(eval OUT  := $(BIN_DIR)/$(BINARY)-$(OS)-$(ARCH)$(EXT))
	@echo "Building $(OUT)..."
	GOOS=$(OS) GOARCH=$(ARCH) go build $(LDFLAGS) -o $(OUT) .
	@scripts/codesign-darwin.sh $(OUT) "$(CODESIGN_IDENTITY)"

endef

## package: Cross-compile, codesign, zip each binary + README, notarize darwin
package: build-all
	$(foreach platform,$(PLATFORMS), \
		$(eval OS   := $(word 1,$(subst /, ,$(platform)))) \
		$(eval ARCH := $(word 2,$(subst /, ,$(platform)))) \
		$(eval EXT  := $(if $(filter windows,$(OS)),.exe,)) \
		$(eval BIN  := $(BIN_DIR)/$(BINARY)-$(OS)-$(ARCH)$(EXT)) \
		$(eval ZIP  := $(BIN_DIR)/$(BINARY)-$(VERSION)-$(OS)-$(ARCH).zip) \
		$(eval STAGE := $(BIN_DIR)/_pkg-$(OS)-$(ARCH)) \
		rm -rf $(STAGE) && mkdir -p $(STAGE) ; \
		cp $(BIN) $(STAGE)/$(BINARY)$(EXT) ; \
		cp README.md $(STAGE)/README.md ; \
		zip -j $(ZIP) $(STAGE)/$(BINARY)$(EXT) $(STAGE)/README.md ; \
		rm -rf $(STAGE) ;)
	@scripts/notarize-darwin.sh $(BIN_DIR)/$(BINARY)-$(VERSION)-darwin-arm64.zip "$(NOTARY_PROFILE)"
	@scripts/notarize-darwin.sh $(BIN_DIR)/$(BINARY)-$(VERSION)-darwin-amd64.zip "$(NOTARY_PROFILE)"

## test: Run all unit tests (hermetic: no ffmpeg needed)
test:
	go test ./...

## clean: Remove build artifacts
clean:
	rm -rf $(BIN_DIR)

## help: Show available targets
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## //'
