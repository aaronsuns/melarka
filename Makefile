.PHONY: test build run web web-test dev
test: ; go test ./...
# Note: `make build` / `go build` embed internal/webui/dist, which in git is only a placeholder page.
# For the real web app use Docker (it builds web/ first) or `make dev` (serves web/dist via LARK_WEB_DIR).
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null | sed 's/^v//' || echo dev)
build: ; CGO_ENABLED=0 go build -ldflags "-X github.com/aaronsuns/lark-server/internal/buildinfo.Version=$(VERSION)" -o bin/lark ./cmd/lark
run: build ; LARK_DATA_DIR=./data LARK_LISTEN=127.0.0.1:4600 LARK_ADMIN_USER=admin LARK_ADMIN_PASSWORD=admin ./bin/lark
web: ; cd web && npm ci && npm run build
web-test: ; cd web && npm test
# Backend on :4600 serving the Vite build from disk; run `cd web && npm run dev` for hot reload on :5173.
dev: build web ; LARK_WEB_DIR=./web/dist LARK_DATA_DIR=./data LARK_LISTEN=127.0.0.1:4600 LARK_ADMIN_USER=admin LARK_ADMIN_PASSWORD=admin ./bin/lark
