SHELL := /bin/bash
DEV_DATABASE_URL := postgres://t3stage:t3stage@127.0.0.1:5439/t3stage?sslmode=disable
# Load .env (if present) into recipe shells; default DATABASE_URL for local dev.
LOAD_ENV := set -a; [ -f .env ] && . ./.env; set +a; export DATABASE_URL="$${DATABASE_URL:-$(DEV_DATABASE_URL)}";

.PHONY: dev db build test up down

## dev: Postgres in compose + Go server (:7380) + Vite dev server (proxies /api)
dev: db
	@$(LOAD_ENV) trap 'kill 0' EXIT; \
	(cd server && go run ./cmd/t3stage) & \
	(cd web && pnpm install && pnpm dev) & \
	wait

## db: start Postgres and wait until healthy
db:
	docker compose up -d --wait db

## build: web bundle embedded into a single Go binary at bin/t3stage
build:
	cd web && pnpm install --frozen-lockfile && pnpm build
	find server/internal/webui/dist -mindepth 1 ! -name .gitkeep -delete
	cp -r web/dist/. server/internal/webui/dist/
	cd server && CGO_ENABLED=0 go build -o ../bin/t3stage ./cmd/t3stage

## test: Go tests + web typecheck/lint
test:
	cd server && go vet ./... && go test ./...
	cd web && pnpm typecheck && pnpm lint

## up: build and run the full stack in compose
up:
	docker compose up -d --build

## down: stop the compose stack (data volume kept)
down:
	docker compose down
