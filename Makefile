
.PHONY: mod
mod:
	go mod vendor -v

.PHONY: lint
lint:
	go tool golangci-lint run

.PHONY: test
test: lint
	go test ./...

.PHONY: all
all: lint test
	@:

.PHONY: generate
generate:
	go generate ./...
