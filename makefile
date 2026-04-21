all: install

LD_FLAGS = -w -s

BUILD_FLAGS := -ldflags '$(LD_FLAGS)'

IMAGE ?= cosmos-pruner:latest

build:
	@echo "Building cosmos-pruner"
	@go build -tags pebbledb -mod readonly $(BUILD_FLAGS) -o build/cosmos-pruner main.go

install:
	@echo "Installing cosmos-pruner"
	@go install -tags pebbledb -mod readonly $(BUILD_FLAGS) ./...

docker:
	docker buildx build --sbom=true --provenance=true --platform linux/amd64 -t $(IMAGE) --push .

clean:
	rm -rf build

.PHONY: all build install docker clean
