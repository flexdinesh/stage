SHELL := /bin/bash
# Dev and tests use compose.dev.yaml (Postgres on 5440), never the prod stack
# (compose.yaml, 5439). Only `up`/`down` touch prod.
DEV_COMPOSE := docker compose -f compose.dev.yaml
DEV_PG := postgres://t3stage:t3stage@127.0.0.1:5440
# One dev database per branch, so worktrees don't share state.
DEV_BRANCH := $(shell git rev-parse --abbrev-ref HEAD 2>/dev/null || echo main)
DEV_DB := t3stage_dev_$(shell printf '%s' '$(DEV_BRANCH)' | tr 'A-Z' 'a-z' | tr -c 'a-z0-9' '_' | cut -c1-40)
# Load .env.dev (if present, never .env) into recipe shells. DATABASE_URL is
# always the branch's dev db.
LOAD_ENV := set -a; [ -f .env.dev ] && . ./.env.dev; set +a; export APP_ENV=dev DATABASE_URL='$(DEV_PG)/$(DEV_DB)?sslmode=disable' APP_PORT="$${APP_PORT:-7390}";

.PHONY: dev db dev-down build test up down

## dev: dev Postgres + branch db + Go server (:7390, real T3) + Vite dev server (proxies /api)
dev: db
	@$(DEV_COMPOSE) exec -T db psql -U t3stage -d postgres -tAc "SELECT 1 FROM pg_database WHERE datname = '$(DEV_DB)'" | grep -q 1 \
		|| $(DEV_COMPOSE) exec -T db createdb -U t3stage '$(DEV_DB)'
	@$(LOAD_ENV) echo "dev db $(DEV_DB), server :$$APP_PORT"; trap 'kill 0' EXIT; \
	(cd server && go run ./cmd/t3stage) & \
	(cd web && pnpm install && pnpm dev) & \
	wait

## db: start the dev/test Postgres (5440) and wait until healthy
db:
	$(DEV_COMPOSE) up -d --wait db

## dev-down: stop the dev/test Postgres (data volume kept)
dev-down:
	$(DEV_COMPOSE) down

## build: web bundle embedded into a single Go binary at bin/t3stage
build:
	cd web && pnpm install --frozen-lockfile && pnpm build
	find server/internal/webui/dist -mindepth 1 ! -name .gitkeep -delete
	cp -r web/dist/. server/internal/webui/dist/
	cd server && CGO_ENABLED=0 go build -o ../bin/t3stage ./cmd/t3stage

## test: Go tests (dev Postgres) + web typecheck/lint
test: db
	cd server && go vet ./... && go test ./...
	cd web && pnpm typecheck && pnpm lint

## up: build and run the production stack in compose
up:
	docker compose up -d --build

## down: stop the production stack (data volume kept)
down:
	docker compose down
