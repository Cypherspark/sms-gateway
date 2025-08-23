APP            := sms
PKG            := ./...
MAIN           := ./cmd/api
GO             := go
GOFLAGS        := 
LDFLAGS        := -s -w
PORT           := 8080
HOST           := 0.0.0.0

DATABASE_URL   ?= postgres://sms:sms@localhost:5432/sms?sslmode=disable
MIGRATIONS_DIR := internal/db/migrations

GOLANGCI_LINT_VERSION := v1.59.1

TEST_FLAGS     := -count=1
RACE_FLAGS     := -race
COVER_PROFILE  := coverage.out
COVER_XML      := coverage.xml

SHELL := /bin/bash

.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"; printf "\n\033[1mTargets\033[0m\n"} /^[a-zA-Z0-9_%-]+:.*?##/ { printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

.PHONY: init
init: ## Go mod download + verify
	$(GO) mod download
	$(GO) mod verify

.PHONY: lint
lint: ## fmt and vet
	go fmt ./...
	go vet ./...
	
.PHONY: build
build: ## Build release binary to ./bin/$(APP)
	mkdir -p bin
	$(GO) build -trimpath -ldflags="$(LDFLAGS)" -o ./bin/$(APP) $(MAIN)

.PHONY: run
run: ## Run API locally (uses DATABASE_URL, HOST, PORT)
	HOST=$(HOST) PORT=$(PORT) DATABASE_URL="$(DATABASE_URL)" $(GO) run $(MAIN)

.PHONY: test
test: ## Run unit tests (no containers), with race + coverage
	$(GO) test $(PKG) $(TEST_FLAGS) $(RACE_FLAGS) -coverprofile=$(COVER_PROFILE) -covermode=atomic

.PHONY: cover
cover: ## Open HTML coverage report
	@[ -f $(COVER_PROFILE) ] || (echo "No $(COVER_PROFILE) found; run 'make test' first." && exit 1)
	$(GO) tool cover -html=$(COVER_PROFILE)

.PHONY: cover-xml
cover-xml: ## Convert coverage to XML (requires gocov & gocov-xml)
	@command -v gocov >/dev/null 2>&1 || go install github.com/axw/gocov/gocov@latest
	@command -v gocov-xml >/dev/null 2>&1 || go install github.com/AlekSi/gocov-xml@latest
	@cat $(COVER_PROFILE) | gocov convert | gocov-xml > $(COVER_XML)
	@echo "Wrote $(COVER_XML)"

.PHONY: bench
bench: ## Run micro-benchmarks
	$(GO) test $(PKG) -run=^$$ -bench=. -benchmem

.PHONY: sqlc
sqlc: ## Generate sqlc code
	sqlc generate

.PHONY: db-up
db-up: ## Start Postgres via docker-compose
	docker compose up -d postgres

.PHONY: db-down
db-down: ## Stop Postgres container
	docker compose down

.PHONY: db-psql
db-psql: ## Open psql shell to DATABASE_URL
	@command -v psql >/dev/null 2>&1 || { echo "psql not found"; exit 1; }
	psql $(DATABASE_URL)

.PHONY: migrate
migrate: ## Apply SQL migrations with psql against DATABASE_URL
	@command -v psql >/dev/null 2>&1 || { echo "psql not found"; exit 1; }
	@for f in $$(ls -1 $(MIGRATIONS_DIR)/*.sql | sort); do \
		echo "Applying $$f"; \
		psql $(DATABASE_URL) -v ON_ERROR_STOP=1 -f $$f; \
	done
	@echo "Migrations applied."

# ---- Quality Gates ---------------------------------------------------------
.PHONY: check
check: sqlc tidy fmt vet lint test ## Full local quality gate (unit tests)

# ---- Cleaning --------------------------------------------------------------
.PHONY: clean
clean: ## Remove binaries & coverage
	rm -rf bin
	rm -f $(COVER_PROFILE) $(COVER_XML)
