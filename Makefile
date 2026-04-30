PREFIX ?= /usr/local
BIN    ?= $(PREFIX)/bin

.PHONY: all build install uninstall clean test fmt

all: build

build:
	go build -trimpath -ldflags="-s -w" -o alien .

install: build
	install -d $(BIN)
	install -m 0755 alien $(BIN)/alien

uninstall:
	rm -f $(BIN)/alien

clean:
	rm -f alien

fmt:
	gofmt -w .

test:
	go test ./...
