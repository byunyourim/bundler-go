.PHONY: build run test lint tidy

build:
	go build -o bin/bundler ./cmd/bundler

run:
	go run ./cmd/bundler

test:
	go test ./...

lint:
	golangci-lint run

tidy:
	go mod tidy
