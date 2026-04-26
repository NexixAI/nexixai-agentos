# syntax=docker/dockerfile:1
FROM golang:1.24-alpine AS build
WORKDIR /src
RUN apk add --no-cache git ca-certificates
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/agentos ./cmd/agentos

FROM alpine:3.20
RUN apk add --no-cache ca-certificates curl && \
    addgroup -S agentos && adduser -S agentos -G agentos && \
    mkdir -p /data && chown agentos:agentos /data
COPY --from=build /out/agentos /usr/local/bin/agentos
WORKDIR /data
USER agentos
HEALTHCHECK --interval=30s --timeout=3s --retries=3 \
    CMD curl -sf http://localhost:${AGENTOS_HEALTH_PORT:-8081}/v1/health || exit 1
ENTRYPOINT ["agentos"]
