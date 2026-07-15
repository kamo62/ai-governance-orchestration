# syntax=docker/dockerfile:1

ARG GO_VERSION=1.26.5

FROM golang:${GO_VERSION}-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN for bin in catalog-validator governance-shell orchestrator ai-orch mcp-stub; do \
		CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/$bin ./cmd/$bin || exit 1; \
	done

FROM alpine:3.22
# Backend control (POST /v1/backends) shells out to docker compose. The CLI is
# excluded from the default image; docker-compose.backend-control.yml opts in.
ARG INSTALL_DOCKER_CLI=false
RUN if [ "$INSTALL_DOCKER_CLI" = "true" ]; then apk add --no-cache docker-cli docker-cli-compose; fi
RUN adduser -D -H aiagent
WORKDIR /app

COPY --from=build /out/ /usr/local/bin/
COPY agents /app/agents
COPY models /app/models
COPY policies /app/policies
COPY mcp /app/mcp
RUN mkdir -p /app/var/audit && chown -R aiagent:aiagent /app/var

USER aiagent
EXPOSE 8080
CMD ["governance-shell"]
