GO_DIR := ai-agent-orch
BIN_DIR := bin
BINARIES := ai-orch governance-shell orchestrator catalog-validator mcp-stub
COMPOSE := docker compose -f docker-compose.yml

.PHONY: build test lint fmt vet staticcheck vuln catalog up down smoke bridge-test clean

build:
	cd $(GO_DIR) && for bin in $(BINARIES); do \
		go build -o $(BIN_DIR)/$$bin ./cmd/$$bin || exit 1; \
	done

test:
	cd $(GO_DIR) && go test ./...

fmt:
	cd $(GO_DIR) && test -z "$$(gofmt -l . | grep -v node_modules)" || (gofmt -l . | grep -v node_modules; exit 1)

vet:
	cd $(GO_DIR) && go vet ./...

staticcheck:
	cd $(GO_DIR) && go run honnef.co/go/tools/cmd/staticcheck@2025.1 ./...

vuln:
	cd $(GO_DIR) && go run golang.org/x/vuln/cmd/govulncheck@v1.3.0 ./...

lint: fmt vet staticcheck

catalog:
	cd $(GO_DIR) && go run ./cmd/catalog-validator -catalog-root .

up:
	cd $(GO_DIR) && $(COMPOSE) up -d --build

down:
	cd $(GO_DIR) && $(COMPOSE) down --remove-orphans

smoke:
	cd $(GO_DIR) && $(COMPOSE) -f docker-compose.beta.yml --profile beta run --rm --no-deps beta-smoke

bridge-test:
	cd $(GO_DIR)/agent-bridge && bun install --frozen-lockfile && bun run test && bun run typecheck && bun run lint

clean:
	rm -rf $(GO_DIR)/$(BIN_DIR)
