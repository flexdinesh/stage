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
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/t3stage ./cmd/t3stage

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=server /out/t3stage /t3stage
ENTRYPOINT ["/t3stage"]
