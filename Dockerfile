# syntax=docker/dockerfile:1

FROM node:26-alpine AS web
WORKDIR /src/web
RUN npm install -g pnpm@12.15.0
COPY web/package.json web/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm build

FROM golang:1.27-alpine AS server
WORKDIR /src/server
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
COPY --from=web /src/web/dist/ ./internal/webui/dist/
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/stage ./cmd/stage
# Data dir for the SQLite database, owned by distroless nonroot (65532), so a
# named volume mounted at /data starts out writable.
RUN mkdir -p /out/data && chown 65532:65532 /out/data && chmod 0700 /out/data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=server /out/stage /stage
COPY --from=server --chown=65532:65532 /out/data /data
ENV STAGE_DATA_DIR=/data
# Reachable through a published port (the default is container loopback).
ENV STAGE_LISTEN=0.0.0.0:7380
ENTRYPOINT ["/stage"]
