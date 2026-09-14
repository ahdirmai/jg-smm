# MVP-1-SMM — top-level task runner.
# Full target set (migrate/seed/test) grows in P0-04/P0-11; this covers the
# P0-02 walking skeleton.

COMPOSE ?= docker compose
# Local worker replicas. Max 3 on a 16 GB machine (INFRA_ANALYST.md §15.2).
WORKERS ?= 3

.PHONY: help up down logs ps bootstrap typecheck test build

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
