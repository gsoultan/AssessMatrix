# AssessMatrix — run `make` to see everything available.
.DEFAULT_GOAL := help
.PHONY: help up down dev check build preview e2e e2e-core e2e-setup load clean core-test proto

help: ## Show this help
	@echo "AssessMatrix"
	@echo
	@grep -E '^[a-z-]+:.*?## .*$$' $(MAKEFILE_LIST) \
	  | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[1m%-10s\033[0m %s\n", $$1, $$2}'
	@echo
	@echo "  Override the port with:  PORT=3000 make dev"

up: ## Everything: Postgres, migrations, a demo admin, the core and the web apps, hot reload
	@./scripts/up.sh

down: ## Stop the Postgres that `make up` started (RESET=1 also wipes its data)
	@./scripts/down.sh

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

e2e-core: ## The same loop against the Go core and Postgres (needs AM_E2E_* URLs)
	@./scripts/e2e-core.sh

e2e-setup: ## First run on a fresh install through /setup (needs AM_E2E_SETUP_* URLs of a throwaway database)
	@./scripts/e2e-setup.sh

load: ## Cohort load test against a local core: N candidates, nothing lost (needs AM_E2E_* URLs)
	@./scripts/load.sh

clean: ## Remove build output and node_modules
	@./scripts/clean.sh

core-test: ## Go tests against Postgres (needs AM_TEST_* URLs; see services/core/README.md)
	@cd services/core && go vet ./... && go test -race -count=1 ./...

proto: ## Lint the schema and regenerate the Go and TypeScript clients
	@cd packages/proto && PATH="$$PATH:$$(go env GOPATH)/bin:$$PWD/node_modules/.bin" sh -c 'buf lint && buf generate'
