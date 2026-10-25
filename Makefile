.PHONY: test check build linux release

VERSION ?= dev

test:
	go test -race ./...

check: test
	go vet ./...
	python3 scripts/check_format.py

build:
	mkdir -p bin
	go build -trimpath -o bin/itmo-schedule-mcp ./cmd/itmo-schedule-mcp
	go build -trimpath -o bin/smoke ./cmd/smoke
	go build -trimpath -ldflags='-X main.version=$(VERSION)' -o bin/itmo-calendar-sync ./cmd/itmo-calendar-sync

linux:
	mkdir -p bin
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o bin/itmo-schedule-mcp-linux-amd64 ./cmd/itmo-schedule-mcp
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w -X main.version=$(VERSION)' -o bin/itmo-calendar-sync-linux-amd64 ./cmd/itmo-calendar-sync

release:
	python3 scripts/release.py --version '$(VERSION)'
