# syntax=docker/dockerfile:1

# pnpm workspace: the shared app (packages/app), the browser host (apps/web) and the desktop
# host frontend (desktop/app/ui, manifest only), one lockfile. The install is
# filtered to the browser host, so the website (apps/website) is not copied.
FROM node:26-alpine AS web
WORKDIR /src
RUN npm install -g pnpm@12.15.0
COPY package.json pnpm-workspace.yaml pnpm-lock.yaml ./
COPY packages/app/package.json packages/app/
COPY apps/web/package.json apps/web/
COPY desktop/app/ui/package.json desktop/app/ui/
RUN pnpm install --frozen-lockfile --filter '@stage/web...'
COPY packages/app/ packages/app/
COPY apps/web/ apps/web/
RUN pnpm --filter @stage/web build

FROM golang:1.27-alpine AS server
WORKDIR /src/server
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
COPY --from=web /src/apps/web/dist/ ./internal/webui/dist/
# The only build with the postgres tag (docs/storage.md): the image runs on
# SQLite by default or on Postgres when STAGE_DATABASE_URL is set.
# Release, desktop and CLI builds never contain the Postgres driver.
RUN CGO_ENABLED=0 go build -tags postgres -trimpath -ldflags="-s -w" -o /out/stage ./cmd/stage
# Data dir (SQLite database, or on Postgres only the lock and sockets), owned by distroless nonroot (65532), so a
# named volume mounted at /data starts out writable.
RUN mkdir -p /out/data && chown 65532:65532 /out/data && chmod 0700 /out/data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=server /out/stage /stage
COPY --from=server --chown=65532:65532 /out/data /data
ENV STAGE_DATA_DIR=/data
# Reachable through a published port (the default is container loopback).
ENV STAGE_LISTEN=0.0.0.0:7380
# A container never runs harnesses that are local processes (docs/runtime.md).
ENV STAGE_MACHINE_PROFILE=container
ENTRYPOINT ["/stage"]
