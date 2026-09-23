BINARY  := s32ftp
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: all build test test-race vet fmt lint clean run docker-build docker-up docker-down

all: build

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/s32ftp

test:
	go test -count=1 ./...

test-race:
	go test -race -count=1 ./...

vet:
	go vet ./...

fmt:
	go fmt ./...

lint: vet
	@command -v golangci-lint >/dev/null 2>&1 && golangci-lint run || echo "golangci-lint not installed, skipping"

clean:
	rm -rf bin

run: build
	./bin/$(BINARY) -config configs/config.example.yaml

docker-build:
	docker build --build-arg VERSION=$(VERSION) -t $(BINARY):$(VERSION) .

docker-up:
	docker compose up --build -d

docker-down:
	docker compose down

hash-password:
	@read -s -p "password: " p; echo; ./bin/$(BINARY) -hash-password "$$p"
