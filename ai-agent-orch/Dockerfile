# syntax=docker/dockerfile:1

ARG GO_VERSION=1.26.4

FROM golang:${GO_VERSION}-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/catalog-validator ./cmd/catalog-validator \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/governance-shell ./cmd/governance-shell \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/openrouter-smoke ./cmd/openrouter-smoke \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/opencode-smoke ./cmd/opencode-smoke \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/beta-gateway-smoke ./cmd/beta-gateway-smoke \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/orchestrator ./cmd/orchestrator \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/ai-orch ./cmd/ai-orch \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/mcp-repo-classification ./cmd/mcp-repo-classification \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/mcp-engineering-standards-kb ./cmd/mcp-engineering-standards-kb \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/mcp-catalog-introspection ./cmd/mcp-catalog-introspection \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/mcp-playwright-cli ./cmd/mcp-playwright-cli \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/mcp-issue-tracker ./cmd/mcp-issue-tracker \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/mcp-documentation ./cmd/mcp-documentation \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/mcp-test-management ./cmd/mcp-test-management

FROM alpine:3.22
RUN apk add --no-cache docker-cli docker-cli-compose
RUN adduser -D -H aiagent
WORKDIR /app

COPY --from=build /out/catalog-validator /usr/local/bin/catalog-validator
COPY --from=build /out/governance-shell /usr/local/bin/governance-shell
COPY --from=build /out/openrouter-smoke /usr/local/bin/openrouter-smoke
COPY --from=build /out/opencode-smoke /usr/local/bin/opencode-smoke
COPY --from=build /out/beta-gateway-smoke /usr/local/bin/beta-gateway-smoke
COPY --from=build /out/orchestrator /usr/local/bin/orchestrator
COPY --from=build /out/ai-orch /usr/local/bin/ai-orch
COPY --from=build /out/mcp-repo-classification /usr/local/bin/mcp-repo-classification
COPY --from=build /out/mcp-engineering-standards-kb /usr/local/bin/mcp-engineering-standards-kb
COPY --from=build /out/mcp-catalog-introspection /usr/local/bin/mcp-catalog-introspection
COPY --from=build /out/mcp-playwright-cli /usr/local/bin/mcp-playwright-cli
COPY --from=build /out/mcp-issue-tracker /usr/local/bin/mcp-issue-tracker
COPY --from=build /out/mcp-documentation /usr/local/bin/mcp-documentation
COPY --from=build /out/mcp-test-management /usr/local/bin/mcp-test-management
COPY agents /app/agents
COPY models /app/models
COPY policies /app/policies
COPY mcp /app/mcp
RUN mkdir -p /app/var/audit && chown -R aiagent:aiagent /app/var

USER aiagent
EXPOSE 8080
CMD ["governance-shell"]
