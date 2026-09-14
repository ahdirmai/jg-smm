# MVP-1-SMM — top-level task runner.
# Full target set (seed) grows in P0-11; this covers P0-02 (stack) and
# P0-04 (migrations).

COMPOSE ?= docker compose
# Local worker replicas. Max 3 on a 16 GB machine (INFRA_ANALYST.md §15.2).
WORKERS ?= 3

# Migrations run in a throwaway container so the DB URL stays the single source
# of truth in compose.yaml (host port 24543, not the in-network 5432).
DB_URL ?= postgres://smm:smm@postgres:5432/smm?sslmode=disable
MIGRATE_RUN = $(COMPOSE) run --rm --no-deps migrate

.PHONY: help up down logs ps bootstrap typecheck test build migrate migrate-down migrate-create migrate-status

help: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

up: ## Bring the whole stack up (idempotent), with $(WORKERS) worker replicas
	$(COMPOSE) up -d --scale worker=$(WORKERS)

down: ## Stop the stack (keeps volumes)
	$(COMPOSE) down

logs: ## Tail logs (S=service to filter, e.g. `make logs S=api`)
	$(COMPOSE) logs -f $(S)

ps: ## Show stack status
	$(COMPOSE) ps

build: ## Build all images
	$(COMPOSE) build

bootstrap: ## Install JS deps and Go modules
	pnpm install
	cd apps/api && go mod download

typecheck: ## Typecheck TS workspaces
	pnpm typecheck

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
