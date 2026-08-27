.PHONY: build test run

build:
	go build -o bin/xbet-api ./cmd/server

test:
	go test ./...

race:
	go test -race ./...

run: build
	./bin/xbet-api -addr :8080
