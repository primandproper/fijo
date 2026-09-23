# ENVIRONMENT
PWD      := $(shell pwd)
MYSELF   := $(shell id -u)
MY_GROUP := $(shell id -g)

# PATHS
THIS          := github.com/primandproper/fijo
BINARY_NAME   := fijo
CMD_PACKAGE   := $(THIS)/cmd/main
ARTIFACTS_DIR := artifacts
SCRIPTS_DIR   := scripts
FRONTEND_DIR  := frontend
COVERAGE_OUT  := $(ARTIFACTS_DIR)/coverage.out

# COMPUTED
TOTAL_PACKAGE_LIST := `go list $(THIS)/...`

# CONTAINER VERSIONS
LINTER_IMAGE        := golangci/golangci-lint:v2.13.1
SQL_GENERATOR_IMAGE := sqlc/sqlc:1.26.0
SHELLCHECK_IMAGE := koalaman/shellcheck:stable

# COMMANDS
CONTAINER_RUNNER      := docker
RUN_CONTAINER         := $(CONTAINER_RUNNER) run --rm --volume $(PWD):$(PWD) --workdir=$(PWD) --network=host
RUN_CONTAINER_AS_USER := $(RUN_CONTAINER) --user $(MYSELF):$(MY_GROUP)
LINTER                := $(RUN_CONTAINER) $(LINTER_IMAGE) golangci-lint
SQL_GENERATOR         := $(RUN_CONTAINER_AS_USER) $(SQL_GENERATOR_IMAGE)

## non-PHONY folders/files

$(ARTIFACTS_DIR):
	@mkdir -p $(ARTIFACTS_DIR)

## PREREQUISITES

# setup prepares a fresh clone: creates the artifacts dir and downloads the
# module cache. This template does not vendor (primitives-go's dependency tree
# is large); builds and tests run against the module cache.
.PHONY: setup
setup: $(ARTIFACTS_DIR)
	go mod download

# Vendoring targets are provided for consumers who prefer a committed vendor
# tree, but nothing depends on them by default.
.PHONY: clean_vendor
clean_vendor:
	$(SCRIPTS_DIR)/clean_vendor.sh

vendor:
	$(SCRIPTS_DIR)/vendor.sh

.PHONY: revendor
revendor: clean_vendor vendor

## FORMATTING

.PHONY: format_imports
format_imports:
	$(SCRIPTS_DIR)/format_imports.sh $(THIS) $(PWD)

.PHONY: format_go_fieldalignment
format_go_fieldalignment:
	@$(SCRIPTS_DIR)/format_go_fieldalignment.sh

.PHONY: format_go_tag_alignment
format_go_tag_alignment:
	@$(SCRIPTS_DIR)/format_go_tag_alignment.sh

.PHONY: go_fix
go_fix:
	go fix ./...

.PHONY: goimports
goimports:
	$(SCRIPTS_DIR)/goimports.sh

.PHONY: format_golang
format_golang: go_fix goimports format_imports format_go_fieldalignment format_go_tag_alignment
	@$(SCRIPTS_DIR)/format_golang.sh $(PWD)

.PHONY: format
format: format_golang format_frontend

.PHONY: fmt
fmt: format

.PHONY: format_frontend
format_frontend:
	$(MAKE) -C $(FRONTEND_DIR) format

## LINTING

.PHONY: golang_lint
golang_lint:
	@$(SCRIPTS_DIR)/golang_lint.sh $(CONTAINER_RUNNER) $(LINTER_IMAGE) "$(LINTER)"

.PHONY: shellcheck
shellcheck:
	@$(SCRIPTS_DIR)/shellcheck.sh $(CONTAINER_RUNNER) $(SHELLCHECK_IMAGE) $(SCRIPTS_DIR)

# lint_golang is what CI's Go job runs; lint adds the frontend for a developer
# who has both toolchains.
.PHONY: lint_golang
lint_golang: golang_lint shellcheck

.PHONY: lint_frontend
lint_frontend:
	$(MAKE) -C $(FRONTEND_DIR) lint
	$(MAKE) -C $(FRONTEND_DIR) format-check
	$(MAKE) -C $(FRONTEND_DIR) check

.PHONY: lint
lint: lint_golang lint_frontend

## GENERATED FILES

# configs renders the per-environment config files under config/ from their real
# Go objects (cmd/tools/codegen/configs). Commit the output so the checked-in
# JSON stays in lockstep with the code.
.PHONY: configs
configs:
	$(SCRIPTS_DIR)/configs.sh $(THIS)

# envvars regenerates the Go constants for every environment variable that can
# override the configuration, derived from the `env:` tags reachable from
# internal/config's configurations constraint. Commit the output; CI fails a PR
# that renames a config field without regenerating.
.PHONY: envvars
envvars:
	$(SCRIPTS_DIR)/envvars.sh $(THIS)

# proto regenerates the Go bindings under internal/grpc/fijopb and the
# TypeScript bindings under frontend/packages/api-client/src/generated from the
# .proto files under proto/. The platform's schemas are read off the module
# cache, never copied here — see scripts/proto.sh.
.PHONY: proto
proto: proto_golang proto_typescript

.PHONY: proto_golang
proto_golang:
	$(SCRIPTS_DIR)/proto.sh $(THIS) golang
	$(MAKE) format_golang

.PHONY: proto_typescript
proto_typescript: $(FRONTEND_DIR)/node_modules
	$(SCRIPTS_DIR)/proto.sh $(THIS) typescript

# The frontend's dependencies, installed once so proto generation and the
# frontend targets have their tools.
$(FRONTEND_DIR)/node_modules:
	$(MAKE) -C $(FRONTEND_DIR) install

# generated regenerates everything under GENERATED FILES.
# querier renders the sqlc querier each domain declares in sqlc.yaml. A domain
# owns its .sql files and gets a generated/ package beside them.
.PHONY: querier
querier:
	@$(SCRIPTS_DIR)/querier.sh $(CONTAINER_RUNNER) $(SQL_GENERATOR_IMAGE) "$(SQL_GENERATOR)"

.PHONY: generated
generated: configs envvars proto querier

## EXECUTION

# build compiles every package (fast failure on breakage) and then produces the
# binary with version metadata injected via ldflags.
.PHONY: build
build: $(ARTIFACTS_DIR)
	go build $(THIS)/...
	$(SCRIPTS_DIR)/build.sh -o $(ARTIFACTS_DIR)/$(BINARY_NAME) $(CMD_PACKAGE)

# run builds and runs the binary; pass args with `make run ARGS="version"`.
.PHONY: run
run:
	go run $(CMD_PACKAGE) $(ARGS)

# test_golang is what CI's Go job runs; test adds the frontend for a developer
# who has both toolchains.
.PHONY: test_golang
test_golang: $(ARTIFACTS_DIR)
	$(SCRIPTS_DIR)/test.sh

.PHONY: test_frontend
test_frontend: $(FRONTEND_DIR)/node_modules
	$(MAKE) -C $(FRONTEND_DIR) test

.PHONY: test
test: test_golang test_frontend

# container_tests runs the Go suite with the Postgres-backed tests switched on.
# It needs a Docker daemon.
.PHONY: container_tests
container_tests: $(ARTIFACTS_DIR)
	RUN_CONTAINER_TESTS=true $(SCRIPTS_DIR)/test.sh

## FRONTEND

.PHONY: frontend_build
frontend_build: $(FRONTEND_DIR)/node_modules
	$(MAKE) -C $(FRONTEND_DIR) build

.PHONY: frontend_dev
frontend_dev: $(FRONTEND_DIR)/node_modules
	$(MAKE) -C $(FRONTEND_DIR) dev

## LOCAL DEVELOPMENT

# dev_db stands up the Postgres the localdev config points at.
.PHONY: dev_db
dev_db:
	$(CONTAINER_RUNNER) run -d --rm --name fijo-postgres -e POSTGRES_USER=fijo -e POSTGRES_PASSWORD=fijo -e POSTGRES_DB=fijo -p 5432:5432 postgres:17-alpine

.PHONY: dev_db_stop
dev_db_stop:
	$(CONTAINER_RUNNER) stop fijo-postgres

# dev_serve runs the API against config/localdev.json, migrating on boot.
.PHONY: dev_serve
dev_serve:
	go run $(CMD_PACKAGE) serve --config config/localdev.json

.PHONY: dev_worker
dev_worker:
	go run $(CMD_PACKAGE) worker --config config/localdev.json

# dev_seed registers an owner and studio on the local database; pass the
# details through SEED_ARGS. See cmd/tools/devseed.
.PHONY: dev_seed
dev_seed:
	go run $(THIS)/cmd/tools/devseed --config config/localdev.json $(SEED_ARGS)

# dev_bootstrap makes the operator on the local database; pass the details
# through BOOTSTRAP_ARGS. It is the same tool an operator runs once against a
# deployment, and is safe to run again. See cmd/tools/bootstrap.
.PHONY: dev_bootstrap
dev_bootstrap:
	go run $(THIS)/cmd/tools/bootstrap --config config/localdev.json $(BOOTSTRAP_ARGS)
