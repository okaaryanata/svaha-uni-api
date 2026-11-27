GOLANGCI_VERSION := v2.15.0
LINTER := $(CURDIR)/.tools/golangci-lint-$(GOLANGCI_VERSION)
.PHONY: run test build lint fmt check
run:
	go run ./cmd/api
test:
	go test -race ./...
build:
	CGO_ENABLED=0 go build -o bin/svaha-uni-api ./cmd/api
$(LINTER):
	@mkdir -p .tools
	GOBIN=$(CURDIR)/.tools go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION)
	@mv .tools/golangci-lint $(LINTER)
lint: $(LINTER)
	$(LINTER) run ./...
	$(LINTER) fmt --diff > .tools/format.diff
	@test ! -s .tools/format.diff
fmt: $(LINTER)
	$(LINTER) fmt
check: lint test build
	go vet ./...
