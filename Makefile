PREFIX  ?= $(HOME)/.local
BINDIR  ?= $(PREFIX)/bin
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

build:
	go build -trimpath -ldflags '$(LDFLAGS)' -o bt ./cmd/bt

test:
	go vet ./...
	go test ./...

install: test build
	install -Dm755 bt $(BINDIR)/bt
	@echo "installed $(BINDIR)/bt ($(VERSION))"

uninstall:
	rm -f $(BINDIR)/bt

clean:
	rm -f bt

.PHONY: build test install uninstall clean
