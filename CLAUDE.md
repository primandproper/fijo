# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

`github.com/primandproper/fijo` — scheduling for one- and two-person recurring-appointment
businesses, built on `github.com/primandproper/platform-go/v14` (the domain tier: identity,
sign-in, audit, sessions, rbac, ...) over `github.com/primandproper/primitives-go/v2` (the
infrastructure tier). Go 1.27. Read `docs/DESIGN.md` first: it holds the settled facts, the domain
model, what Fijo composes from each tier, and the build order. Phase 0 (scaffold and owner sign-in)
is done; phases 1–6 are ahead.

**The reference consumer is `../dinnerdonebetter`.** When you wonder how something is done on this
stack, read how DDB does it before inventing anything. The two sibling repositories'
`CLAUDE.md` files (`../primitives-go`, `../platform-go`) are binding conventions here too — the
tier rule, store shapes (`Tx` for writes, executor for reads), options seams, error mapping.

**platform-go v14 is frozen.** Anything Fijo needs from the platform is an additive change there or
a filed issue, never a local fork and never platform code written in this repository. The open
issues Fijo depends on are listed in the design doc.

## The one binary

`cmd/main` → `internal/cli`. Every workload is a subcommand: `fijo serve` (gRPC API),
`fijo worker` (background loops), `fijo migrate` (schema + policy seed), `fijo version`. A new
workload is a subcommand and a manifest that names it, not a new `cmd/` directory.

`cmd/tools/` holds tools that are never deployed: the config and envvars renderers, `devseed`,
which registers an owner and their studio on a local database, and `bootstrap`, which an operator
runs once against a deployment to make the first `service_admin`. Both are flag parsing around
`internal/bootstrap`; registering and granting a service role are a registrar's acts, so an empty
directory has no door for either, and what authorizes these is the database credential.

## Layout

- `internal/config/` — `Config` embeds platform-go's `service.Config`, so a platform subsystem is
  composed by being present in the config and by nothing else. Fijo's own settings are the `Auth`
  block. `ValidateForServing` is the stricter check `serve` and `worker` make; `version` and
  `migrate` are valid over a config naming nothing. `Load` overlays `FIJO_` variables on flag
  defaults; `LoadFromFile` decodes JSON and overlays the same variables.
- `internal/build/api/` — the composition root of `serve`, DDB's shape driven by platform-go's
  `service.Register` / `RegisterTransports` / `New`. What it registers by hand is what no
  environment variable can express: hooks, the session store, the sign-in service and flows, the
  interceptors, the authorization requirements. `internal/build/worker/` is the same registration
  with the servers left out plus the runners.
- `internal/bootstrap/` — the two acts an empty database has no authenticated door for:
  provisioning a studio with its owner, and granting `service_admin`. Deliberately not reachable
  over the wire. Its three callers are `cmd/tools/devseed`, `cmd/tools/bootstrap` and container
  tests, which reach it through `internal/testutils/seed`. Every studio after the first goes
  through identity's own `Register` RPC, which an operator is authorized to call.
- `internal/auth/` — the sign-in layer: `principal` (the caller every handler reads; `Scope()` is
  the global directory, `StudioScope()` is Fijo's tenant), `session` (the server-side record behind
  every access token, one per mint), `interceptor` (bearer token → caller in four revocable reads),
  `auditing` (identity + signin hooks recording to the platform audit log, scoped to the studio),
  `mint` (a sign-in for a passkey-proven user — an extraction candidate, see the design doc),
  `passkeyflow`, `resetmail`.
- `internal/authz/roles/` — the `owner` / `staff` / `service_admin` policy rbac seeds on every
  migration. Fijo's own RPCs are all self-service today and declared `Public` to the enforcer;
  the first RPC that acts on somebody else's row brings the first Fijo permission.
- `internal/database/migrations/` — one sequence: platform DDL spliced in by version (1–8), Fijo's
  own files from 20. Cascading FKs onto `identity_users` and the role-name FK onto `authz_roles`
  are added here because the platform packages cannot declare them.
- `internal/domain/<name>/` — a Store interface, sqlc queries in `sqlc_queries/` rendering a
  `generated/` querier, a Postgres implementation, a mock. `owners` is the smallest (user
  preferences); `schedule` is phase 1's grid — offerings, series, occurrences, closures, the
  civil `Date`/`ClockTime` types, the `Materializer` that turns rules into rows, and `Commands`,
  the audited acts that write rows and their audit entry on one transaction. The roster and
  messaging domains follow the same outline.
- `internal/studios/` — what a studio is that identity owns rather than Fijo: today the time zone
  the materializer resolves a wall clock in, behind the narrow interface the domain takes.
- `internal/services/<name>/grpc/` — Fijo's gRPC servers, each with a `Require` declaring its
  policy and a `RegisterOn`. Generated bindings live in `internal/grpc/fijopb` from `proto/fijo/v1`.
- `internal/workers/sweep/` — a `service.Runner` that calls a function on an interval; despite the
  name it is the generic loop. `internal/workers/materialize/` names the schedule's use of it.
- `frontend/owner/` — the SvelteKit owner app; `frontend/packages/api-client/` — ts-proto bindings
  plus promisified clients. `frontend/go.mod` exists only so `./...` never descends into
  `node_modules`.
- `config/` — rendered per-environment JSON; edit `cmd/tools/codegen/configs/environments.go`,
  never the JSON, then `make configs`.

## Common Commands

```bash
make setup           # artifacts dir + module cache
make dev_db          # local Postgres (fijo/fijo/fijo on :5432)
make dev_serve       # fijo serve --config config/localdev.json (migrates on boot)
make dev_seed SEED_ARGS="--username ... --email ... --password ... --studio ..."
make dev_bootstrap BOOTSTRAP_ARGS="--username ... --email ... --password ..."   # the operator
make configs         # render config/<env>.json from the Go builders
make envvars         # regenerate internal/config/envvars
make proto           # regenerate Go + TypeScript bindings (needs protoc 33.1, frontend deps)
make generated       # configs + envvars + proto + querier
make format          # Go + frontend formatting
make lint            # golangci-lint + shellcheck (Docker) + eslint/prettier/svelte-check
make test            # Go unit tests (excludes cmd) + vitest
make container_tests # Go suite with RUN_CONTAINER_TESTS=true (needs Docker)
```

`make test_golang` / `make lint_golang` and `make test_frontend` / `make lint_frontend` are the
halves CI runs. Run a single Go test with `go test -run TestName ./internal/...`.

## Conventions worth knowing

- **Config presence is composition.** A platform block holding nothing but the library's defaults
  is released as unconfigured and never registered; the environment builders set at least one
  field explicitly on every block Fijo depends on. `ValidateForServing` names the missing ones.
- **envDefault beats the file.** Environment overlay applies an `envDefault` over whatever a file
  said. That is why `Auth.Sessions` is Fijo's own struct with the database provider fixed rather
  than the platform's `sessionscfg.Config`, whose `Provider` defaults to `cache`.
- **Tenancy.** The identity directory is `tenancy.Global()` (one directory, studios are accounts in
  it). Fijo rows and audit entries carry the studio: `tenancy.Of(accountID)`. `callers.Principal.Scope()`
  must stay global because identity's gRPC surface reads it as the directory scope.
- **Sessions are one per mint.** A refresh exchange creates a new session row rather than finding
  its predecessor, because `sessions.Store` has no lookup but by identifier; the old row idles out
  and the sweeper removes it. Sign-out deletes the current row and revokes the refresh family.
- **`Config.NewPillars`** builds the three noops itself when nothing is configured, so
  `fijo version` prints nothing but its data; `TestNewPillarsIsQuietWhenUnconfigured` guards it.
  `serve` and `worker` let `service.Register` build their own pillars from the same config.
- **Structured logs go to stdout**, and so does `version`'s output.
- **A wall clock is not an instant.** A series stores a weekday, a minute of the day and a civil
  date; the instant is computed in the studio's zone when an occurrence is materialized. Durations
  are whole minutes or days, because an INTERVAL does not cross `database/sql` without a custom
  type and a TIME column arrives as a string.
- **The grid's history is Fijo's own RPC**, not the platform's mounted audit surface, which derives
  its scope from `Principal.Scope()` and so cannot see a studio's entries (platform-go#875).
- Tests use `shoenig/test` (`test` non-fatal, `must` fatal), call `t.Parallel()` by default, and
  container-backed tests gate on `RUN_CONTAINER_TESTS=true` through primitives-go's `pgtest`;
  `internal/testutils/pgclient` opens a migrated client on one.
- Import ordering is `gci` with four sections: stdlib, this module, `github.com/primandproper`,
  everything else. The Makefile `THIS` variable must be the full module path.
- `scripts/go_files.sh` is the one place deciding which Go files the formatters see; point new
  filesystem-walking tooling at it.

## Linting

- ~46 linters via `.golangci.yml` (golangci-lint v2). Formatters: `gci`, `gofmt`. Notable
  strictness: `errcheck`, `errorlint`, `gosec`, `forcetypeassert`, `unconvert`, `unparam`,
  `goconst`, `gocritic`. Many relaxed for `_test.go`. Generated files are skipped by header.
