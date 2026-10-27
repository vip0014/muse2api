# syntax=docker/dockerfile:1.7
FROM node:24.21.0-bookworm-slim AS web-build
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

FROM golang:1.24.0-bookworm AS go-build
WORKDIR /src
ENV CGO_ENABLED=0 GOOS=linux
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY --from=web-build /src/internal/server/static ./internal/server/static
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go test ./internal/... ./cmd/... && \
    go build -trimpath -ldflags="-s -w" -o /out/muse2api ./cmd/muse2api

FROM alpine:3.22.2
RUN apk add --no-cache ca-certificates tzdata chromium font-noto-cjk && addgroup -g 10001 muse && adduser -D -H -u 10001 -G muse muse
WORKDIR /app
COPY --from=go-build /out/muse2api /usr/local/bin/muse2api
RUN mkdir -p /app/data/media /app/data/backups /app/data/chromium/generate /app/data/downloads \
    && chown -R muse:muse /app \
    && chmod -R go-rwx /app/data
ENV HOME=/app/data/chromium MUSE2API_HOME=/app MUSE2API_HOST=0.0.0.0 MUSE2API_PORT=18610 MUSE2API_BROWSER=1 MUSE2API_CHROMIUM=/usr/bin/chromium MUSE2API_CDP_PORT=19210
USER 10001:10001
VOLUME ["/app/data"]
EXPOSE 18610
STOPSIGNAL SIGTERM
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD wget -q -O /dev/null http://127.0.0.1:18610/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/muse2api"]
