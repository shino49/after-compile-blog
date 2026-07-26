.PHONY: dev-api dev-web test check build

dev-api:
	go run ./cmd/server
dev-web:
	npm --prefix web run dev
test:
	go test ./...
	npm --prefix web test
check:
	gofmt -w cmd internal
	go vet ./...
	npm --prefix web run typecheck
build:
	npm --prefix web run build
	CGO_ENABLED=0 go build -o after-compile ./cmd/server
