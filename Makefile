# AssessMatrix — run `make` to see everything available.
.DEFAULT_GOAL := help
.PHONY: help dev check build preview e2e e2e-core load clean core-test proto

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

e2e-core: ## The same loop against the Go core and Postgres (needs AM_E2E_* URLs)
	@./scripts/e2e-core.sh

load: ## Cohort load test against a local core: N candidates, nothing lost (needs AM_E2E_* URLs)
	@./scripts/load.sh

clean: ## Remove build output and node_modules
	@./scripts/clean.sh

core-test: ## Go tests against Postgres (needs AM_TEST_* URLs; see services/core/README.md)
	@cd services/core && go vet ./... && go test -race -count=1 ./...

proto: ## Lint the schema and regenerate the Go and TypeScript clients
	@cd packages/proto && PATH="$$PATH:$$(go env GOPATH)/bin:$$PWD/node_modules/.bin" sh -c 'buf lint && buf generate'
