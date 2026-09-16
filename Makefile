# MVP-1-SMM — top-level task runner.
# One place to run the stack, migrations, codegen and CI-equivalent checks.
# `make help` lists everything.

COMPOSE ?= docker compose
# Local worker replicas. Max 3 on a 16 GB machine (INFRA_ANALYST.md §15.2).
WORKERS ?= 3

# Bootstrap owner account created by `make seed` (override on the CLI).
SEED_ADMIN_EMAIL ?= owner@smm.local
SEED_ADMIN_PASSWORD ?= changeme-changeme

# Migrations run in a throwaway container so the DB URL stays the single source
# of truth in compose.yaml (host port 24543, not the in-network 5432).
DB_URL ?= postgres://smm:smm@postgres:5432/smm?sslmode=disable
MIGRATE_RUN = $(COMPOSE) run --rm --no-deps migrate

# sqlc runs from its pinned image so the host needs no Go/cgo toolchain.
# Version is pinned here and in apps/api/sqlc.yaml consumers; bump together.
SQLC_VERSION ?= 1.27.0
SQLC_RUN = docker run --rm -v "$(PWD)/apps/api":/src -w /src sqlc/sqlc:$(SQLC_VERSION)

# Code generation from the OpenAPI SSOT (openapi/openapi.yaml).
OAPI_CODEGEN_VERSION ?= v2.4.1

.PHONY: help up env down logs ps bootstrap hooks lint typecheck test build ci fmt-check migrate migrate-down migrate-create migrate-status sqlc sqlc-check seed generate generate-go generate-ts backup restore drill

help: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

up: env ## Bring the whole stack up (idempotent), with $(WORKERS) worker replicas
	$(COMPOSE) up -d --scale worker=$(WORKERS)

up-obs: env ## Bring the app stack up plus the observability tier (Prometheus/Grafana/Loki/Alertmanager)
	COMPOSE_PROFILES=obs $(COMPOSE) up -d --scale worker=$(WORKERS)

up-mail: env ## Bring the app stack up plus the local mailpit catcher (P4-06)
	COMPOSE_PROFILES=mail $(COMPOSE) up -d --scale worker=$(WORKERS)

env: ## Create .env from infra/docker/.env.example if missing
	@if [ ! -f .env ]; then cp infra/docker/.env.example .env; echo "created .env from example"; fi

down: ## Stop the stack (keeps volumes)
	$(COMPOSE) down

logs: ## Tail logs (S=service to filter, e.g. `make logs S=api`)
	$(COMPOSE) logs -f $(S)

ps: ## Show stack status
	$(COMPOSE) ps

build: ## Build all images
	$(COMPOSE) build

bootstrap: ## Install JS deps, Go modules, and wire the git hooks
	pnpm install
	cd apps/api && go mod download
	@$(MAKE) hooks

hooks: ## Point git at the in-repo hooks (.githooks) — gitleaks secret guard
	git config core.hooksPath .githooks
	@echo "git hooks installed from .githooks (pre-commit secret scan)"

typecheck: ## Typecheck TS workspaces
	pnpm typecheck

lint: ## Lint TS workspaces (ESLint 9 flat config)
	pnpm lint

fmt-check: ## Fail if any Go file is not gofmt-clean
	@cd apps/api && unformatted="$$(gofmt -l .)"; \
		if [ -n "$$unformatted" ]; then echo "gofmt found unformatted files:"; echo "$$unformatted"; exit 1; fi; \
		echo "gofmt clean"

ci: lint typecheck fmt-check ## Run the checks CI runs, locally (no containers needed)
	pnpm build
	pnpm test
	cd apps/api && go vet ./... && go build ./... && go test ./...

test: ## Run all tests
	pnpm test
	cd apps/api && go test ./...

migrate: ## Apply all pending migrations (up)
	$(MIGRATE_RUN) -path=/migrations -database="$(DB_URL)" up

migrate-down: ## Roll back the most recent migration (N=2 to roll back two)
	$(MIGRATE_RUN) -path=/migrations -database="$(DB_URL)" down $(or $(N),1)

migrate-status: ## Show migration version and dirty flag
	$(MIGRATE_RUN) -path=/migrations -database="$(DB_URL)" version

migrate-create: ## Create a migration pair (NAME=add_thing)
	@test -n "$(NAME)" || (echo "usage: make migrate-create NAME=add_thing" >&2; exit 1)
	$(MIGRATE_RUN) -ext sql -dir /migrations -seq "$(NAME)"

sqlc: ## Regenerate sqlc query code (apps/api/internal/repository/sqlcgen)
	$(SQLC_RUN) generate

sqlc-check: ## Fail if generated sqlc code is stale (CI)
	$(SQLC_RUN) generate
	@cd apps/api && git diff --exit-code -- internal/repository/sqlcgen || \
		(echo "sqlc output is stale; run 'make sqlc' and commit" >&2; exit 1)

seed: ## Create/refresh the bootstrap owner user (SEED_ADMIN_EMAIL/SEED_ADMIN_PASSWORD)
	$(COMPOSE) run --rm --no-deps \
		-e SEED_ADMIN_EMAIL=$(SEED_ADMIN_EMAIL) \
		-e SEED_ADMIN_PASSWORD=$(SEED_ADMIN_PASSWORD) \
		api seed

generate: generate-go generate-ts ## Regenerate all code from the OpenAPI spec

generate-go: ## Generate Go types from openapi/openapi.yaml
	cd apps/api && go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@$(OAPI_CODEGEN_VERSION) --config oapi-codegen.yaml ../../openapi/openapi.yaml

generate-ts: ## Generate FE types into packages/shared
	pnpm --filter @smm/shared generate:api

backup: ## Back up Postgres + WAL, MinIO and worker sessions to infra/backup/backups
	bash infra/backup/backup.sh

restore: ## Restore from a backup (B=name or timestamp; default: latest). DROPS the live DB
	bash infra/backup/restore.sh "$(or $(B),latest)"

drill: ## DR drill: backup, wipe all app volumes, restore, verify. DESTROYS DATA
	bash infra/backup/drill.sh
