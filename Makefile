# AssessMatrix — run `make` to see everything available.
.DEFAULT_GOAL := help
.PHONY: help dev check build preview e2e clean

help: ## Show this help
	@echo "AssessMatrix"
	@echo
	@grep -E '^[a-z-]+:.*?## .*$$' $(MAKEFILE_LIST) \
	  | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[1m%-9s\033[0m %s\n", $$1, $$2}'
	@echo
	@echo "  Override the port with:  PORT=3000 make dev"

dev: ## Start the UI on http://localhost:5180
	@./scripts/dev.sh

check: ## Type-check only (fast)
	@./scripts/check.sh

build: ## Type-check and build to apps/studio/dist
	@./scripts/build.sh

preview: ## Build, then serve the production bundle
	@./scripts/preview.sh

e2e: ## Smoke-test the create → take → score loop in a browser
	@./scripts/e2e.sh

clean: ## Remove build output and node_modules
	@./scripts/clean.sh
