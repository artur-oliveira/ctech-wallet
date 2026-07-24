BUILD_DIR   := dist
GOOS        ?= linux
GOARCH      ?= arm64
CGO_ENABLED := 0

.PHONY: build-outbound build-webhook test vet clean

build-outbound:
	CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=$(GOARCH) \
		go build -tags lambda.norpc -ldflags="-s -w" -o $(BUILD_DIR)/outbound/bootstrap ./cmd/outbound

build-webhook:
	CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=$(GOARCH) \
		go build -tags lambda.norpc -ldflags="-s -w" -o $(BUILD_DIR)/webhook/bootstrap ./cmd/webhook

test:
	go test ./... -race -coverprofile=coverage.out

vet:
	go vet ./...

clean:
	rm -rf $(BUILD_DIR)
