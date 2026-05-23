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
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/orchestrator ./cmd/orchestrator

FROM alpine:3.22
RUN adduser -D -H aiagent
WORKDIR /app

COPY --from=build /out/catalog-validator /usr/local/bin/catalog-validator
COPY --from=build /out/governance-shell /usr/local/bin/governance-shell
COPY --from=build /out/openrouter-smoke /usr/local/bin/openrouter-smoke
COPY --from=build /out/orchestrator /usr/local/bin/orchestrator
COPY agents /app/agents
COPY models /app/models
RUN mkdir -p /app/var/audit && chown -R aiagent:aiagent /app/var

USER aiagent
EXPOSE 8080
CMD ["governance-shell"]
