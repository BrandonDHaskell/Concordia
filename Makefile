BIN := concordiad
CONFIG ?= deploy/config.example.toml

.PHONY: build test lint run migrate clean

build:
	go build -o bin/$(BIN) ./cmd/$(BIN)

test:
	go test ./...

lint:
	go vet ./...
	@command -v staticcheck >/dev/null && staticcheck ./... || echo "staticcheck not installed, skipping"

run: build
	./bin/$(BIN) -config $(CONFIG)

migrate: build
	./bin/$(BIN) -config $(CONFIG) -migrate

clean:
	rm -rf bin
