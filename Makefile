.PHONY: build test test-coverage lint clean docker-build docker-up docker-down

build:
	go build -o bin/server ./cmd/server

test:
	go test ./... -v -race

test-coverage:
	go test ./... -coverprofile=coverage.out
	go tool cover -func=coverage.out

lint:
	golangci-lint run ./...

clean:
	rm -rf bin/ coverage.out

docker-build:
	docker build -t glm-proxy-go .

docker-up:
	docker compose up -d --build

docker-down:
	docker compose down

# Quick check: build + test
check: build test
