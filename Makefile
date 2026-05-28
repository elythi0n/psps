BINARY  := psps
BIN     := build
MODULE  := github.com/elythi0n/psps

# Effective version — read from VERSION file if present, else "v0.0.0".
VERSION_FILE := VERSION
VERSION      ?= $(shell [ -f $(VERSION_FILE) ] && cat $(VERSION_FILE) || echo v0.0.0)

# Build flags. -X stamps the version into the cli package at link time.
LDFLAGS := -s -w -X $(MODULE)/internal/cli.version=$(VERSION)

# Install prefix. /usr/local needs sudo; use install-user for ~/.local/bin.
PREFIX ?= /usr/local

# ── Default ─────────────────────────────────────────────────────────────────

## Default target — builds the binary
.PHONY: all
all: build

# ── Build ───────────────────────────────────────────────────────────────────

## Build the psps binary → ./build/psps
.PHONY: build
build:
	@echo "building $(BINARY) $(VERSION)..."
	@mkdir -p $(BIN)
	go build -trimpath -ldflags='$(LDFLAGS)' -o $(BIN)/$(BINARY) .
	@echo "binary: $(BIN)/$(BINARY)"

## Build and run with the live kitty.conf
.PHONY: run
run: build
	./$(BIN)/$(BINARY)

## Install the binary to $(PREFIX)/bin (default /usr/local/bin — needs sudo)
.PHONY: install
install: build
	@if ! install -Dm755 $(BIN)/$(BINARY) $(DESTDIR)$(PREFIX)/bin/$(BINARY) 2>/dev/null; then \
		echo "permission denied writing to $(DESTDIR)$(PREFIX)/bin"; \
		echo "  → run    : sudo make install"; \
		echo "  → or    : make install-user      (installs to ~/.local/bin, no sudo)"; \
		echo "  → or    : make pkg                (Arch: build + install as a pacman package)"; \
		exit 1; \
	fi
	@echo "installed: $(DESTDIR)$(PREFIX)/bin/$(BINARY)"

## Install the binary to ~/.local/bin (no sudo). Make sure ~/.local/bin is on $$PATH.
.PHONY: install-user
install-user: build
	install -Dm755 $(BIN)/$(BINARY) $(HOME)/.local/bin/$(BINARY)
	@echo "installed: $(HOME)/.local/bin/$(BINARY)"
	@case ":$$PATH:" in *":$(HOME)/.local/bin:"*) ;; \
		*) echo ""; echo "warning: $(HOME)/.local/bin is not on \$$PATH — add it to your shell rc";; \
	esac

## Remove the installed binary
.PHONY: uninstall
uninstall:
	rm -f $(DESTDIR)$(PREFIX)/bin/$(BINARY)
	rm -f $(HOME)/.local/bin/$(BINARY)
	@echo "removed: $(DESTDIR)$(PREFIX)/bin/$(BINARY) and ~/.local/bin/$(BINARY)"

# ── Quality ─────────────────────────────────────────────────────────────────

## Tidy go.mod / go.sum
.PHONY: tidy
tidy:
	go mod tidy

## Run go vet across all packages
.PHONY: vet
vet:
	go vet ./...

## Format all Go sources
.PHONY: fmt
fmt:
	gofmt -w .

## Run all tests
.PHONY: test
test:
	go test ./...

## Run staticcheck (downloads via `go run` if not installed)
.PHONY: staticcheck
staticcheck:
	go run honnef.co/go/tools/cmd/staticcheck@latest ./...

## Run golangci-lint (requires golangci-lint installed; see .golangci.yml)
.PHONY: lint
lint:
	golangci-lint run

## Tidy + vet + test + staticcheck (everything that should pass before pushing)
.PHONY: check
check: tidy vet test staticcheck

## Validate .goreleaser.yml without building
.PHONY: release-check
release-check:
	go run github.com/goreleaser/goreleaser/v2@latest check

## Local release dry-run — builds into ./dist/, does not publish anywhere.
## Useful for inspecting archive layout and nfpm packages before tagging.
.PHONY: release-snapshot
release-snapshot:
	go run github.com/goreleaser/goreleaser/v2@latest release --snapshot --clean

# ── Versioning ──────────────────────────────────────────────────────────────

## Set a new version (writes VERSION, commits, tags). usage: make tag V=v0.2.0
.PHONY: tag
tag:
	@if [ -z "$(V)" ]; then echo "usage: make tag V=v0.2.0"; exit 1; fi
	@case "$(V)" in v[0-9]*) ;; *) echo "version must start with 'v' (e.g. v0.2.0)"; exit 1 ;; esac
	@echo "$(V)" > $(VERSION_FILE)
	@git add $(VERSION_FILE)
	@git commit -m "release $(V)"
	@git tag -a "$(V)" -m "release $(V)"
	@echo "tagged $(V) — run 'make push' to publish it"

# ── Git ─────────────────────────────────────────────────────────────────────

## Push the current branch + all tags to the configured remote (no remote setup is done here)
.PHONY: push
push:
	@if ! git rev-parse --abbrev-ref HEAD >/dev/null 2>&1; then \
		echo "not a git repository — run: git init -b main"; exit 1; \
	fi
	@if ! git remote get-url origin >/dev/null 2>&1; then \
		echo "no 'origin' remote — set one first, e.g.:"; \
		echo "  git remote add origin git@github.com:<user>/$(BINARY).git"; exit 1; \
	fi
	@branch=$$(git rev-parse --abbrev-ref HEAD); \
	echo "pushing $$branch + tags → $$(git remote get-url origin)"; \
	git push -u origin "$$branch"; \
	git push origin --tags

# ── Arch package ────────────────────────────────────────────────────────────

## Update the pkgver in PKGBUILD from the current VERSION (strips leading 'v')
.PHONY: pkg-sync
pkg-sync:
	@if [ ! -f PKGBUILD ]; then echo "PKGBUILD missing"; exit 1; fi
	@pkgver=$$(echo "$(VERSION)" | sed 's/^v//'); \
	sed -i "s/^pkgver=.*/pkgver=$$pkgver/" PKGBUILD; \
	echo "PKGBUILD pkgver=$$pkgver"

## Build and install the Arch package locally (runs makepkg -si)
.PHONY: pkg
pkg: pkg-clean pkg-sync
	makepkg -si

## Build the Arch package only (no install)
.PHONY: pkg-build
pkg-build: pkg-clean pkg-sync
	makepkg -f

## Clean any stale makepkg staging dirs and downloaded sources
.PHONY: pkg-clean
pkg-clean:
	@chmod -R u+rwX pkg src 2>/dev/null || true
	@rm -rf pkg src $(BINARY)-*.tar.gz *.pkg.tar.zst *.pkg.tar.xz

## Refresh sha256 checksums in PKGBUILD (only relevant if you add remote sources)
.PHONY: pkg-checksums
pkg-checksums:
	updpkgsums

# ── Utilities ───────────────────────────────────────────────────────────────

## Remove build artifacts and makepkg outputs
.PHONY: clean
clean: pkg-clean
	rm -rf $(BIN)

## Print this help message
.PHONY: help
help:
	@echo ""
	@echo "usage: make <target>"
	@echo ""
	@awk '/^##/{desc=substr($$0,3); next} /^[a-zA-Z_-]+:/{printf "  \033[36m%-20s\033[0m %s\n", $$1, desc; desc=""}' $(MAKEFILE_LIST)
	@echo ""
	@echo "versioning:"
	@echo "  current version  : $(VERSION)"
	@echo "  bump example     : make tag V=v0.2.0  &&  make push"
	@echo ""
	@echo "first push to github:"
	@echo "  git remote add origin git@github.com:<user>/$(BINARY).git"
	@echo "  git add -A && git commit -m 'initial'"
	@echo "  make push"
	@echo ""
