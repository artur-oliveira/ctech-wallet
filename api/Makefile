# Deployed binary MUST be named `app` — CDK userdata expects /opt/app/current/app.
BINARY      := app
BUILD_DIR   := dist
GOOS        ?= linux
GOARCH      ?= arm64
CGO_ENABLED := 0

.PHONY: build lint test test-integration vet clean

build:
	CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=$(GOARCH) \
		go build -ldflags="-s -w" -o $(BUILD_DIR)/$(BINARY) ./cmd/server

reconcile:
	CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=$(GOARCH) \
		go build -ldflags="-s -w" -o $(BUILD_DIR)/reconcile ./cmd/reconcile

lint:
	golangci-lint run ./...

test:
	go test ./... -race -coverprofile=coverage.out

# Requires DynamoDB Local: docker compose -f docker-compose.test.yml up -d
test-integration:
	DYNAMODB_ENDPOINT=http://localhost:8123 \
		go test -tags integration -race -count=1 ./tests/integration/ -v

vet:
	go vet ./...

clean:
	rm -rf $(BUILD_DIR)
