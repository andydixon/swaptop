BIN     := bin
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION:v%=%)
PREFIX  ?= /usr/local

.PHONY: build install man test vet fmt brew-formula repo clean

build:
	go build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN)/swaptop .

install: build
	install -Dm755 $(BIN)/swaptop $(DESTDIR)$(PREFIX)/bin/swaptop
	install -Dm644 docs/swaptop.1 $(DESTDIR)$(PREFIX)/share/man/man1/swaptop.1

man:
	man -l docs/swaptop.1

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

# Homebrew formula for the tagged release. The tag must already be pushed:
# the checksum is of GitHub's source tarball for that tag.
BREW_REPO := https://github.com/andydixon/swaptop
brew-formula:
	@set -e; v=$(VERSION); v=$${v#v}; \
	case $$v in *-*) echo "brew-formula: HEAD is not exactly a release tag ($$v)"; exit 1;; esac; \
	url=$(BREW_REPO)/archive/refs/tags/v$$v.tar.gz; \
	sum=$$(curl -fsSL "$$url" | sha256sum | cut -d' ' -f1) || { echo "cannot fetch $$url (tag pushed?)"; exit 1; }; \
	mkdir -p $(BIN)/homebrew; \
	sed -e '/^#/d' -e "s/@VERSION@/$$v/g" -e "s/@SHA256@/$$sum/" packaging/homebrew/swaptop.rb.in > $(BIN)/homebrew/swaptop.rb; \
	echo "wrote $(BIN)/homebrew/swaptop.rb (v$$v, sha256 $$sum)"

# Build .deb/.rpm/Arch packages for the newest tag and publish to repo.dixon.cx.
repo:
	packaging/repo.sh

clean:
	rm -rf $(BIN)
