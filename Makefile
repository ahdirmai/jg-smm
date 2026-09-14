# MVP-1-SMM — top-level task runner.
# Full target set (up/down/migrate/seed/test) lands in P0-11. This is the P0-01 stub.

.PHONY: help bootstrap typecheck test

help: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

bootstrap: ## Install JS deps and Go modules
	pnpm install
	cd apps/api && go mod download

typecheck: ## Typecheck TS workspaces
	pnpm typecheck

test: ## Run all tests
	pnpm test
	cd apps/api && go test ./...
