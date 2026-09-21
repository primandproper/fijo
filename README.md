# template-go

A batteries-included Go application template built on
[`primandproper/primitives-go`](https://github.com/primandproper/primitives-go).

Unlike a bare scaffold, this template ships a **real, runnable application**: a
[Cobra](https://github.com/spf13/cobra) CLI that bootstraps the primitives-go
observability suite (logging, tracing, metrics, profiling) with graceful
shutdown, plus the full build/format/lint/test toolchain and CI to go with it.

The CLI is meant to be your single entrypoint. Building a one-off tool? Add a
subcommand. A long-running worker? Add a subcommand. An HTTP service? Add a
`serve` subcommand that stands up `primitives-go`'s HTTP server. You start here.

`primitives-go` is the infrastructure tier: the providers behind an interface
that every service is built from. When the application grows a domain — users,
billing, audit, webhooks — that is
[`platform-go`](https://github.com/primandproper/platform-go), which requires
this module's dependency and adds a composition root (`service`) over it. The
template does not depend on it, because a template has no domain yet.

## Quickstart

Requires **Go 1.27+**. [Docker](https://www.docker.com/) is used for linting and
shellcheck.

```bash
make setup                  # create artifacts/ and download the module cache
make build                  # compile everything, produce artifacts/template-go
./artifacts/template-go version
./artifacts/template-go --help
```

Or run without building a binary:

```bash
make run ARGS="version"
```

## What's included

- **A working CLI** — `cmd/main` → `internal/cli` (cobra root + `version`
  subcommand), wired to the observability suite in `internal/config`.
- **Observability out of the box** — structured slog logging; tracing, metrics,
  and profiling default to noop so the binary is quiet and dependency-free until
  you turn them on, which is a config change rather than a code edit.
- **Config as compiled objects** — `config/*.json` is rendered from real, typed,
  validated Go values (`make configs`), never hand-edited.
- **Environment variables you can't typo** — `make envvars` derives every
  variable that can override the config and emits it as Go constants.
- **`Makefile` + `scripts/`** — thin Makefile delegating to shellcheck-clean
  scripts for build, format, lint, and test.
- **`.golangci.yml`** — ~46 linters (golangci-lint v2), with `gci` + `gofmt`
  formatters and a strict-but-practical policy.
- **GitHub Actions** — `build`, `formatting`, `lint`, `shellcheck`, and
  `unit tests`, each mirroring a `make` target and path-filtered.
- **`CLAUDE.md`**, issue/PR templates, and a Go `.gitignore`.

## Common commands

```bash
make format     # imports (gci), field/tag alignment, gofmt -s
make lint       # golangci-lint (Docker) + shellcheck (Docker)
make test       # go test -shuffle -race -vet=all -failfast (excludes cmd)
make build      # compile all packages + build the binary with version metadata
make generated  # re-render config/*.json and the envvars constants
```

## Configuration

The CLI's own persistent flags, each defaulting from an environment variable:

| Flag             | Environment variable          | Default       | Values                           |
| ---------------- | ----------------------------- | ------------- | -------------------------------- |
| `--log-level`    | `TEMPLATE_GO_LOG_LEVEL`       | `info`        | `debug`, `info`, `warn`, `error` |
| `--service-name` | `TEMPLATE_GO_SERVICE_NAME`    | `template-go` | any string                       |
| `--config`       | `TEMPLATE_GO_CONFIG_FILEPATH` | unset         | path to a JSON config file       |

With `--config` set, that file is loaded in place of the flag/env defaults and
`TEMPLATE_GO_`-prefixed variables are overlaid on top of it. The files under
`config/` are rendered from real, typed, validated Go objects in
`cmd/tools/codegen/configs` — edit the Go, then `make configs`, never the JSON.

```bash
TEMPLATE_GO_LOG_LEVEL=debug ./artifacts/template-go version
```

Observability logs are structured slog written to **stdout**. The `version`
subcommand prints its data to stdout and emits nothing at the default `info`
level, so `template-go version` stays machine-parseable.

### Turning on telemetry

Tracing, metrics and profiling are off until one of them names a provider, and
naming one is all it takes — no code edit:

```bash
TEMPLATE_GO_OBSERVABILITY_TRACING_PROVIDER=otel \
TEMPLATE_GO_OBSERVABILITY_TRACING_OTELGRPC_COLLECTOR_ENDPOINT=localhost:4317 \
  ./artifacts/template-go version
```

`internal/config/envvars` is the full list, generated from the `env:` tags
reachable from the config rather than written down by hand:

```bash
make envvars    # regenerate after changing the Config struct
```

That package exists because the failure it prevents is invisible. A variable one
underscore off its tag is simply not read — the default stands, and the process
comes up healthy and wrong. Referring to
`envvars.ObservabilityTracingProviderEnvVarKey` in a deployment manifest puts a
compiler in front of that mistake. The walk starts from `internal/config`'s
`configurations` constraint, so a new loadable config struct cannot skip it.

## Layout

```
cmd/main/             # entrypoint: signal-cancellable context -> cli.Execute
cmd/tools/codegen/    # the generators behind `make configs` and `make envvars`
config/               # generated per-environment JSON, committed and reviewable
internal/cli/         # cobra root command, observability bootstrap, subcommands
internal/config/      # assembles observability.Config and builds the pillars
internal/config/envvars/  # generated: every env var that can override the config
version/              # build metadata, injected via -ldflags by scripts/build.sh
scripts/              # build/format/lint/test/shellcheck helpers
.github/workflows/    # CI mirroring the make targets
```

## Make it yours

After creating a repository from this template, run the rename script with your
new module path. It rewrites every reference to this template's module path and
app name, reformats the code, and then deletes itself — leaving no trace that the
project started from a template:

```bash
./rename.sh github.com/acme/coolapp
```

Then confirm everything is wired up:

```bash
make setup && make build && make test
```

<details>
<summary>What the script changes (in case you prefer to do it by hand)</summary>

- **`go.mod`** — the `module` path.
- **`Makefile`** — `THIS` (full module path) and `BINARY_NAME`.
- **`.golangci.yml`** — the `prefix(...)` entries under `formatters.gci.sections`
  (this module and its org).
- **`scripts/`** — the module path in `scripts/test.sh` and
  `scripts/format_imports.sh`, and `VERSION_PKG` in `scripts/build.sh`.
- **`internal/`** — `DefaultServiceName` and the `TEMPLATE_GO_*` env-var prefixes.
- **`CLAUDE.md`** and this **`README.md`** — project details.

The `Makefile` `THIS` variable must be the full module path, because
`scripts/format_imports.sh` runs `dirname` on it to derive the org-level import
prefix (section 3 of the `gci` ordering). `primitives-go` (also under
`github.com/primandproper`) intentionally moves to the third-party import group
once your module lives under a different org.
</details>

## License

[AGPL-3.0](./LICENSE).
