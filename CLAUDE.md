# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

`github.com/primandproper/template-go` — a batteries-included Go application template built on
[`github.com/primandproper/primitives-go/v2`](https://github.com/primandproper/primitives-go). Go 1.27.

The application is a **Cobra CLI** that acts as the single entrypoint. Out of the box it bootstraps
the primitives-go observability suite (logging/tracing/metrics/profiling) with graceful shutdown and
ships a `version` subcommand. Grow it by adding subcommands (e.g. a `serve` command that stands up
`primitives-go`'s HTTP server).

**Which repository a dependency comes from.** primitives-go is the infrastructure tier — the
providers behind an interface that every service is built from, and nothing in it owns a table.
`github.com/primandproper/platform-go/v14` is the domain tier (identity, billing, audit, webhooks,
and `service`, its composition root). This template requires only primitives-go, because a template
has no domain yet; platform-go joins when the application grows one. Everything this repo used to
import from `platform-go/v11` — `observability`, `config`, `version` — now lives in primitives-go,
so a v11-era import path is always a stale one rather than a second place to look.

## Layout

- `cmd/main/main.go` — thin entrypoint: signal-cancellable context → `cli.Execute`.
- `cmd/tools/codegen/configs/` — codegen tool behind `make configs`: builds each environment's
  `*config.Config` as a real, typed Go object (`environments.go`) and renders it to
  `config/<env>.json` via primitives-go's `config.RenderJSONFiles`, which validates and marshals
  every environment before it writes any of them. The checked-in JSON is a projection of these
  builders — edit the Go, never the JSON, then re-run `make configs`.
- `cmd/tools/codegen/envvars/` — codegen tool behind `make envvars`: derives every environment
  variable that can override the configuration and writes it to `internal/config/envvars` as Go
  constants. It walks `internal/config`'s `configurations` constraint rather than a list of type
  names, so the output is complete by construction — a config struct cannot become loadable without
  appearing there. It prints an advisory stderr line naming the dependency modules it discovered and
  did not parse; that is expected here, where only primitives-go supplies config structs.
- `config/` — generated per-environment config files (`localdev.json`, `production.json`); committed so
  they stay reviewable, and loadable at runtime via `--config`.
- `internal/cli/` — cobra root command, observability bootstrap + shutdown, subcommands.
- `internal/config/` — assembles `observability.Config` and builds the pillars. Two loaders use
  `primitives-go/v2/config`: `Load` overlays `TEMPLATE_GO_`-prefixed environment variables on the
  flag/default-seeded config, and `LoadFromFile` decodes a complete JSON config file and then
  overlays the same environment variables. Rendering goes the other way and is primitives-go's
  `config.RenderJSONFiles` (see `make configs`).
- `internal/config/envvars/` — generated; do not edit. Regenerate with `make envvars`.
- `version/` — build metadata (`CommitHash`/`BuildTime`/`CommitTime`), injected via `-ldflags` by
  `scripts/build.sh`.

## Common Commands

```bash
make setup          # Create artifacts dir + download the module cache
make configs        # Render config/<env>.json from the real Go objects in cmd/tools/codegen/configs
make envvars        # Regenerate internal/config/envvars from the config's env: tags
make generated      # configs + envvars
make build          # Compile all packages, then build artifacts/template-go with version metadata
make run ARGS="version"   # go run the CLI with arguments
make format         # Format all Go code (imports, field alignment, tag alignment, gofmt)
make lint           # Run golangci-lint (Docker) + shellcheck
make test           # Run tests (race detector, shuffle, failfast); excludes cmd packages
```

Run a single test:
```bash
go test -run TestName ./internal/config/...
```

Linting runs in Docker (`golangci/golangci-lint` image). Formatting runs locally via `go tool` with
`gci`, `goimports`, `fieldalignment`, `tagalign`, and `gofmt` (declared in the `tool` block of go.mod).

This template does **not** vendor dependencies (primitives-go's dependency tree is large); builds and
tests run against the module cache. Vendoring targets (`make vendor` / `make revendor`) exist for
consumers who want them.

`scripts/go_files.sh` is the one place that decides which Go files the formatters see, and
`format_golang.sh`, `format_imports.sh`, `goimports.sh`, and the `gofmt` check in
`.github/workflows/formatting.yaml` all take their list from it. It asks the Go toolchain
(`go list -e -f '{{.Dir}}' ./...`, then `find -maxdepth 1`) rather than writing out an exclusion
list, so `vendor/`, `testdata/`, and any `_`- or `.`-prefixed directory are skipped for the same
reason `go test ./...` skips them. Point new filesystem-walking tooling at it rather than adding a
fourth spelling of the same exclusion.

Two things about it are load-bearing. It **fails loudly rather than emitting an empty list** — an
out-of-sync `vendor/modules.txt` makes `go list` exit non-zero, and a formatter that quietly formats
nothing (or a CI check that quietly checks nothing) is worse than a stop. And its callers read it
**through a file, not `< <(...)`**, because process substitution discards the exit status of what it
runs, which is exactly how that empty list would go unnoticed.

## Import Ordering

Import ordering uses `gci` with four sections, separated by blank lines:

1. Standard library
2. `github.com/primandproper/template-go` (this module)
3. `github.com/primandproper` (org-level packages, including primitives-go)
4. Everything else (third-party)

The Makefile `THIS` variable must be the full module path (`github.com/primandproper/template-go`)
because `format_imports.sh` runs `dirname` on it to derive the org-level prefix.

## Testing

- Tests use `shoenig/test`: `test` for non-fatal assertions, `must` for fatal ones. Both take
  `(t, expected, actual)` and annotate failures via `test.Sprintf` / `must.Sprintf` settings rather
  than `...f` variants.
- Tests call `t.Parallel()` by default.
- `make test` excludes `cmd` packages, so keep testable logic in `internal/` and `version/`.
- Test command: `CGO_ENABLED=1 go test -shuffle=on -race -vet=all -failfast`.

## Conventions worth knowing

- Observability logs are structured slog written to **stdout**. `version` prints its data to stdout
  and emits nothing at the default `info` level, so `template-go version` stays machine-parseable.
- The `--log-level` / `--service-name` persistent flags default from the `TEMPLATE_GO_LOG_LEVEL` and
  `TEMPLATE_GO_SERVICE_NAME` environment variables. The `--config` flag (default from
  `TEMPLATE_GO_CONFIG_FILEPATH`) points at a JSON config file; when set, `bootstrap` loads it via
  `config.LoadFromFile` instead of the flag/env defaults.
- Configuration is layered: defaults (or a JSON file) < `TEMPLATE_GO_`-prefixed environment variables.
  Env vars follow primitives-go's nested `envPrefix` tags, e.g.
  `TEMPLATE_GO_OBSERVABILITY_LOGGING_LEVEL`. Give new `Config` fields both `envPrefix`/`env` and `json`
  tags so they participate in `Load` and `LoadFromFile`.
- To enable real tracing/metrics/profiling, name a provider in the Tracing/Metrics/Profiling
  sub-config — in the JSON or via `TEMPLATE_GO_OBSERVABILITY_*_PROVIDER`. No code edit is needed:
  `Config.NewPillars` hands a configured config to `observability.Config.NewPillars`, exporters and
  all.
- `Config.NewPillars` builds the three noops itself when nothing is configured, and that branch is
  load-bearing rather than a leftover. primitives-go's tracing sub-config logs an unconditional
  `"tracing disabled"` at info on its way to the noop (its two siblings are silent), structured logs
  go to stdout, and so does `version` — so delegating unconditionally would put a log line in front
  of every `template-go version` and cost the documented machine-parseable output. No setting
  silences it. `TestNewPillarsIsQuietWhenUnconfigured` is the guard; do not "simplify" that branch
  away.

## Linting

- ~46 linters enabled via `.golangci.yml` (golangci-lint v2 format).
- Formatters: `gci` and `gofmt` (configured in the `formatters:` section).
- Notable strictness: `errcheck` (with `check-blank` + `check-type-assertions`), `errorlint`,
  `gosec`, `forcetypeassert`, `unconvert`, `unparam`. Many are relaxed for `_test.go` files.
