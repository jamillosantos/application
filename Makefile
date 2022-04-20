
.PHONY: mod
mod:
	go mod vendor -v

.PHONY: lint
lint:
	go run github.com/golangci/golangci-lint/cmd/golangci-lint run

.PHONY: sec
sec:
	go run github.com/securego/gosec/v2/cmd/gosec ./...

.PHONY: test
test: lint
	go test ./...

.PHONY: all
all: lint sec test
	@:

.PHONY: generate
generate:
	go generate ./...
