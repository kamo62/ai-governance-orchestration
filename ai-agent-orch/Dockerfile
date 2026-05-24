# syntax=docker/dockerfile:1

ARG GO_VERSION=1.26

FROM golang:${GO_VERSION}-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/catalog-validator ./cmd/catalog-validator \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/governance-shell ./cmd/governance-shell \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/openrouter-smoke ./cmd/openrouter-smoke \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/orchestrator ./cmd/orchestrator \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/ai-orch ./cmd/ai-orch \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/mcp-repo-classification ./cmd/mcp-repo-classification \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/mcp-engineering-standards-kb ./cmd/mcp-engineering-standards-kb \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/mcp-catalog-introspection ./cmd/mcp-catalog-introspection \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/mcp-playwright-cli ./cmd/mcp-playwright-cli

FROM alpine:3.22
RUN adduser -D -H aiagent
WORKDIR /app

COPY --from=build /out/catalog-validator /usr/local/bin/catalog-validator
COPY --from=build /out/governance-shell /usr/local/bin/governance-shell
COPY --from=build /out/openrouter-smoke /usr/local/bin/openrouter-smoke
COPY --from=build /out/orchestrator /usr/local/bin/orchestrator
COPY --from=build /out/ai-orch /usr/local/bin/ai-orch
COPY --from=build /out/mcp-repo-classification /usr/local/bin/mcp-repo-classification
COPY --from=build /out/mcp-engineering-standards-kb /usr/local/bin/mcp-engineering-standards-kb
COPY --from=build /out/mcp-catalog-introspection /usr/local/bin/mcp-catalog-introspection
COPY --from=build /out/mcp-playwright-cli /usr/local/bin/mcp-playwright-cli
COPY agents /app/agents
COPY models /app/models
COPY policies /app/policies
COPY mcp /app/mcp
RUN mkdir -p /app/var/audit && chown -R aiagent:aiagent /app/var

USER aiagent
EXPOSE 8080
CMD ["governance-shell"]
