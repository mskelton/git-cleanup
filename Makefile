.PHONY: test install fmt build

test:
	@go test ./...

install:
	@go install .

fmt:
	@go fmt ./...

build:
	@./scripts/build.sh
