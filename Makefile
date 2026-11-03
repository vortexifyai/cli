GO ?= go
VERSION ?= dev
LDFLAGS = -X vortexify-cli/cmd.version=$(VERSION)

.PHONY: build test release
build:
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o dist/vortexify .

test:
	$(GO) test ./...

release:
	@mkdir -p dist
	@set -e; for target in darwin/arm64 darwin/amd64 linux/amd64 linux/arm64 windows/amd64; do \
		os=$${target%/*}; arch=$${target#*/}; suffix=""; \
		if [ "$$os" = windows ]; then suffix=".exe"; fi; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o "dist/vortexify-$$os-$$arch$$suffix" .; \
	done
