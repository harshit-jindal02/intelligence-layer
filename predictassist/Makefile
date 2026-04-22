.PHONY: build build-predictassist build-shopsim build-loadgen \
       docker-up docker-down migrate clean test proto lint

# Binary output directory
BIN_DIR := bin

# Go build flags
GO_BUILD := go build -trimpath
LDFLAGS  := -ldflags "-s -w"

## build: Build all binaries
build: build-predictassist build-shopsim build-loadgen

## build-predictassist: Build the predictassist server binary
build-predictassist:
	$(GO_BUILD) $(LDFLAGS) -o $(BIN_DIR)/predictassist ./cmd/predictassist

## build-shopsim: Build the shop simulator binary
build-shopsim:
	$(GO_BUILD) $(LDFLAGS) -o $(BIN_DIR)/shopsim ./shopsim/cmd

## build-loadgen: Build the load generator binary
build-loadgen:
	$(GO_BUILD) $(LDFLAGS) -o $(BIN_DIR)/loadgen ./cmd/loadgen

## docker-up: Start all services via Docker Compose
docker-up:
	docker compose -f deployments/docker-compose.yml up -d

## docker-down: Stop all services via Docker Compose
docker-down:
	docker compose -f deployments/docker-compose.yml down

## migrate: Run database migrations
migrate:
	go run ./cmd/migrate

## clean: Remove build artifacts
clean:
	rm -rf $(BIN_DIR)
	go clean -cache -testcache

## test: Run all tests with race detection
test:
	go test -race -cover ./...

## proto: Regenerate protobuf / gRPC stubs
proto:
	@echo "Generating protobuf stubs..."
	buf generate

## lint: Run linters
lint:
	golangci-lint run ./...
