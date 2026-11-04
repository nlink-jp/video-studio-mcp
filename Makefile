MODULE  := github.com/nlink-jp/video-studio-mcp
BINARY  := video-studio-mcp
BIN_DIR := dist

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-X $(MODULE)/cmd.Version=$(VERSION)"

# macOS Developer ID signing / notarization (see CONVENTIONS.md §Code
# Signing). Defaults match any Developer ID Application cert in the keychain
# and the org-standard notary profile. Builds without these fall back to
# ad-hoc / un-notarized with a one-line warning.
CODESIGN_IDENTITY ?= Developer ID Application
NOTARY_PROFILE    ?= nlink-jp-notary

# ffmpeg is a runtime dependency on every platform, so unlike voice-studio-mcp
# this server ships the linux/windows targets too. darwin ships arm64 only
# (no amd64, no universal), per the org Release Archive Standard.
PLATFORMS := \
	linux/amd64 \
	linux/arm64 \
	darwin/arm64 \
	windows/amd64

.PHONY: build build-all package verify-release verify-skill-zip test package-skill install-skill uninstall-skill clean help

## build: Build binary for the current OS/Arch → ./dist/video-studio-mcp
build:
	@mkdir -p $(BIN_DIR)
	go build $(LDFLAGS) -o $(BIN_DIR)/$(BINARY) .
	@scripts/codesign-darwin.sh $(BIN_DIR)/$(BINARY) "$(CODESIGN_IDENTITY)"

## build-all: Cross-compile every release platform (codesign darwin build)
build-all:
	@mkdir -p $(BIN_DIR)
	@for p in $(PLATFORMS); do os=$${p%/*}; arch=$${p#*/}; \
		ext=""; [ "$$os" = windows ] && ext=".exe"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build $(LDFLAGS) -o $(BIN_DIR)/$(BINARY)-$$os-$$arch$$ext . ; \
	done
	@scripts/codesign-darwin.sh $(BIN_DIR)/$(BINARY)-darwin-arm64 "$(CODESIGN_IDENTITY)" "$(BINARY)"

## package: Build all platforms, archive with version suffix (zip for
## darwin/windows, tar.gz for linux), bundle the canonical binary +
## README.md + LICENSE + FONTS_LICENSE (bundled M PLUS 1p caption font),
## and notarize the darwin build. Asset naming follows the org Release
## Archive Standard (video-studio-mcp-vX.Y.Z-<os>-<arch>.<ext>).
## Also builds the skill zip (package-skill): every release carries both.
package: build-all package-skill
	@cd $(BIN_DIR) && for p in $(PLATFORMS); do os=$${p%/*}; arch=$${p#*/}; \
		ext=""; [ "$$os" = windows ] && ext=".exe"; \
		stage=_pkg; rm -rf $$stage; mkdir -p $$stage; \
		cp "$(BINARY)-$$os-$$arch$$ext" "$$stage/$(BINARY)$$ext"; \
		cp ../README.md ../LICENSE ../FONTS_LICENSE $$stage/; \
		base="$(BINARY)-$(VERSION)-$$os-$$arch"; \
		if [ "$$os" = linux ]; then ( cd $$stage && COPYFILE_DISABLE=1 tar --no-xattrs -czf "../$$base.tar.gz" * ); \
		else ( cd $$stage && zip -q "../$$base.zip" * ); fi; \
		rm -rf $$stage; \
	done
	@scripts/notarize-darwin.sh $(BIN_DIR)/$(BINARY)-$(VERSION)-darwin-arm64.zip "$(NOTARY_PROFILE)"

## verify-release: refuse to release a zip that is un-notarized, stale, does
## not unpack, does not run, or holds a build from another tag, and a linux
## archive that carries macOS metadata or anything but its canonical files.
## Every step fails closed; only the spctl line is informational.
## Its prerequisite verify-skill-zip refuses a release without the skill zip.
verify-release: verify-skill-zip
	@test -f "$(BIN_DIR)/$(BINARY)-$(VERSION)-darwin-arm64.zip.notarized" || { \
		echo "verify-release: FAIL — $(BINARY)-$(VERSION)-darwin-arm64.zip has no notarization marker."; \
		echo "  make package must end with '[notarize] ...: Accepted'. Do not upload this zip."; \
		exit 1; }
	@test "$(BIN_DIR)/$(BINARY)-$(VERSION)-darwin-arm64.zip.notarized" -nt "$(BIN_DIR)/$(BINARY)-$(VERSION)-darwin-arm64.zip" || { \
		echo "verify-release: FAIL — the zip was rebuilt after its marker (re-run make package)."; \
		exit 1; }
	@tmp=$$(mktemp -d); rc=0; \
		if ! unzip -oq "$(BIN_DIR)/$(BINARY)-$(VERSION)-darwin-arm64.zip" -d "$$tmp"; then \
			echo "verify-release: FAIL — the zip does not unpack. Do not upload it."; rc=1; \
		elif ! out=$$("$$tmp/$(BINARY)" --version 2>&1); then \
			echo "verify-release: FAIL — the packaged binary does not run:"; \
			echo "  $$out"; rc=1; \
		elif ! printf '%s\n' "$$out" | grep -qF "$(VERSION)"; then \
			echo "verify-release: FAIL — the packaged binary reports \"$$out\", not $(VERSION)."; \
			echo "  The zip holds a build from another tag (re-run make package)."; rc=1; \
		else \
			echo "  $$out"; \
			spctl -a -vv -t install "$$tmp/$(BINARY)" 2>&1 | head -2 || true; \
		fi; \
		rm -rf "$$tmp"; \
		exit $$rc
	@for p in $(PLATFORMS); do os=$${p%/*}; arch=$${p#*/}; \
		[ "$$os" = linux ] || continue; \
		f="$(BIN_DIR)/$(BINARY)-$(VERSION)-$$os-$$arch.tar.gz"; \
		names=$$(tar --options 'tar:!mac-ext' -tzf "$$f") || { echo "verify-release: FAIL — $$f does not list."; exit 1; }; \
		if printf '%s\n' "$$names" | grep -qE '(^|/)(\._|PaxHeader|__MACOSX)'; then \
			echo "verify-release: FAIL — $$f carries macOS metadata entries."; \
			echo "  macOS tar writes ._ members unless COPYFILE_DISABLE=1 is set, and lists them only with !mac-ext."; \
			exit 1; fi; \
		xh=$$(python3 -c 'import sys, tarfile; print(" ".join(m.name for m in tarfile.open(sys.argv[1]) if any(k.startswith(("LIBARCHIVE.xattr.", "SCHILY.xattr.")) for k in m.pax_headers)))' "$$f") || { \
			echo "verify-release: FAIL — $$f cannot be read for its pax headers (python3 tarfile)."; exit 1; }; \
		if [ -n "$$xh" ]; then \
			echo "verify-release: FAIL — $$f carries extended attributes as pax headers ($$xh)."; \
			echo "  macOS tar writes them unless called with --no-xattrs; COPYFILE_DISABLE alone does not."; \
			exit 1; fi; \
		got=$$(printf '%s\n' "$$names" | LC_ALL=C sort | tr '\n' ' '); \
		want=$$(printf '%s\n' "$(BINARY)" README.md LICENSE FONTS_LICENSE | LC_ALL=C sort | tr '\n' ' '); \
		if [ "$$got" != "$$want" ]; then \
			echo "verify-release: FAIL — $$f holds $$got; expected $$want"; exit 1; fi; \
	done
	@echo "verify-release: OK ($(VERSION), notarized, unpacks, runs, reports its version, clean linux archives)"

## test: Run all unit tests (hermetic: no ffmpeg needed) and the skill validator
test:
	go test ./...
	@tests/validate-skill.sh

SKILLS_DEST ?= $(HOME)/.claude/skills
SKILL_NAME  := presentation-video
SKILL_ZIP   := $(BIN_DIR)/$(SKILL_NAME)-$(VERSION).zip

## package-skill: Zip the bundled skill → dist/presentation-video-<version>.zip.
## The zip's root is the skill folder — the layout the app's skill registration
## accepts and the skills-series repositories ship. A separate release asset.
package-skill:
	@tests/validate-skill.sh >/dev/null
	@mkdir -p $(BIN_DIR)
	@rm -f "$(SKILL_ZIP)"
	@zip -qrX "$(SKILL_ZIP)" "$(SKILL_NAME)" -x '*.DS_Store'
	@echo "packaged: $(SKILL_ZIP)"

## verify-skill-zip: refuse a release whose skill zip is missing, holds anything
## but the tracked files of the skill at its root, or differs from them.
verify-skill-zip:
	@test -f "$(SKILL_ZIP)" || { \
		echo "verify-skill-zip: FAIL — $(SKILL_ZIP) is missing. make package builds it; every release carries it."; \
		exit 1; }
	@got=$$(unzip -Z1 "$(SKILL_ZIP)" | grep -v '/$$' | LC_ALL=C sort); \
		want=$$(git ls-files "$(SKILL_NAME)" | LC_ALL=C sort); \
		if [ "$$got" != "$$want" ]; then \
			echo "verify-skill-zip: FAIL — $(SKILL_ZIP) does not hold exactly the tracked files of $(SKILL_NAME)/, at its root."; \
			echo "  An untracked file in $(SKILL_NAME)/ ends up in the zip too; remove it or commit it, then re-run make package."; \
			exit 1; fi
	@tmp=$$(mktemp -d); rc=0; \
		if unzip -q "$(SKILL_ZIP)" -d "$$tmp"; then \
			for f in $$(git ls-files "$(SKILL_NAME)"); do \
				cmp -s "$$tmp/$$f" "$$f" || { echo "verify-skill-zip: FAIL — $$f in $(SKILL_ZIP) differs from the working tree (re-run make package)."; rc=1; break; }; \
			done; \
		else echo "verify-skill-zip: FAIL — $(SKILL_ZIP) does not unpack."; rc=1; fi; \
		rm -rf "$$tmp"; \
		exit $$rc
	@echo "verify-skill-zip: OK ($(SKILL_ZIP))"

## install-skill: Install the skill from the built zip into SKILLS_DEST
## (default ~/.claude/skills). Unpacking the zip rather than copying the source
## tree means a packaging defect breaks this install instead of a release.
install-skill: package-skill
	@mkdir -p "$(SKILLS_DEST)"
	rm -rf "$(SKILLS_DEST)/$(SKILL_NAME)"
	@unzip -q "$(SKILL_ZIP)" -d "$(SKILLS_DEST)"
	@echo "installed from $(SKILL_ZIP) -> $(SKILLS_DEST)/$(SKILL_NAME)"

## uninstall-skill: Remove the installed presentation-video skill
uninstall-skill:
	rm -rf "$(SKILLS_DEST)/$(SKILL_NAME)"
	@echo "removed: $(SKILLS_DEST)/$(SKILL_NAME)"

## clean: Remove build artifacts
clean:
	rm -rf $(BIN_DIR)

## help: Show available targets
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## //'

# Homebrew tap generation (see scripts/release-brew.mk). After `make package`,
# `make brew` generates this formula from the built darwin-arm64 zip into the
# local nlink-jp/homebrew-tap checkout. The package target is unchanged.
BREW_KIND := formula
BREW_DESC := MCP server compositing page images and audio into a narrated MP4
include scripts/release-brew.mk
