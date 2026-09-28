# fijo

Scheduling for one- and two-person recurring-appointment businesses: the weekly
grid of standing appointments with exceptions, cancellations and make-ups, and
the texts that keep a household showing up. The first tenant is a music studio;
nothing music-specific lives in the core.

Fijo is built on [`primandproper/platform-go`](https://github.com/primandproper/platform-go)
(the domain tier: identity, sign-in, audit, sessions, ...) over
[`primandproper/primitives-go`](https://github.com/primandproper/primitives-go)
(the infrastructure tier: database, observability, gRPC, WebAuthn, email, ...),
and it follows [dinnerdonebetter](https://github.com/primandproper/dinnerdonebetter)
as the reference consumer of that stack. The design is in
[`docs/DESIGN.md`](docs/DESIGN.md); this file is how to run it.

## What is here today (phase 0)

- **One deployed binary, four workloads.** `fijo serve` is the gRPC API,
  `fijo worker` the background loops, `fijo migrate` the schema, `fijo version`
  the build. Adding a workload means a subcommand, not a new `cmd/` directory.
- **The platform composed by config.** A subsystem is switched on by being
  present in `config/<env>.json` and by nothing else — see
  [`internal/config`](internal/config) and platform-go's `service` package.
- **Owner sign-in.** Password and passkey, refresh tokens with reuse detection,
  password reset over email, a server-side session behind every access token,
  and sign-out that revokes on the next request. Every identity and sign-in
  event lands in the audit log, scoped to the studio.
- **Roles.** `owner` and `staff` on a studio membership, `service_admin` on the
  operator, seeded from Go on every migration.
- **Time zones from day one.** The studio's on the identity account, the
  person's in Fijo's `user_preferences` side table.
- **A SvelteKit owner app** under [`frontend/owner`](frontend/owner) whose server
  calls the Go API over gRPC, with the API's tokens in one encrypted cookie.

Not here yet: the grid, the roster, texts, money. Those are phases 1–5 in the
design.

## Quickstart

The whole local stack needs nothing but [Docker](https://www.docker.com/):

```bash
make dev        # Postgres, migrations, API, worker and owner app, in the foreground
```

That builds the binaries once, migrates, starts `fijo serve` (gRPC on :50051)
and `fijo worker`, seeds an owner, and serves the owner app at
http://localhost:5173/login. Sign in as `maria` / `a good long password`. Ctrl-C
stops everything; `make dev` again rebuilds the Go side, and frontend edits hot
reload. `make dev_reset` drops the database. Ports and the seeded owner are
overridable. See the header of `deploy/environments/localdev/docker-compose.yaml`.

Working outside Docker (tests, linting, codegen) needs **Go 1.27+**, **Node 22+
with npm 11+** and **protoc 33.1**. The pieces also run one at a time against
the compose stack's Postgres (`docker compose -f
deploy/environments/localdev/docker-compose.yaml up postgres`): `make dev_serve`, `make dev_seed SEED_ARGS=...`, and
`make frontend_dev` with `frontend/owner/.env` copied from `.env.example` and
`COOKIE_ENCRYPTION_KEY` set.

Passkeys work on `localhost` because the localdev relying party names it. The
localdev mailer drops every message, so a password-reset link is visible only
in the API's debug log.

## Common commands

```bash
make format          # Go (gci, alignment, gofmt) + frontend (prettier)
make lint            # golangci-lint + shellcheck (Docker) + eslint, prettier, svelte-check
make test            # go test -shuffle -race -vet=all (excludes cmd) + vitest
make container_tests # the Go suite with the Postgres-backed tests switched on
make build           # compile all packages + build artifacts/fijo with version metadata
make generated       # configs + envvars + proto
make proto           # regenerate Go and TypeScript bindings from proto/
```

`make test_golang`, `make lint_golang`, `make test_frontend` and `make
lint_frontend` are the halves CI runs separately.

## Layout

```
cmd/main/                      the binary
cmd/tools/codegen/{configs,envvars}   config renderers
cmd/tools/devseed/             registers the first owner on a local database
internal/cli/                  root, version, serve, worker, migrate
internal/config/               Config = platform service.Config + Fijo's Auth block
internal/build/{api,worker}/   composition roots
internal/auth/                 principal, session, interceptor, auditing, mint,
                               passkeyflow, resetmail
internal/authz/roles/          the policy rbac seeds
internal/database/migrations/  the one migration sequence, platform DDL spliced in
internal/domain/owners/        user preferences (the time zone)
internal/services/{ownerauth,owners}/grpc   Fijo's two gRPC services
internal/grpc/fijopb/          generated from proto/fijo/v1
internal/workers/sweep/        the credential sweepers the worker runs
proto/fijo/v1/                 OwnerAuthService, OwnersService
frontend/owner/                the SvelteKit owner app
frontend/packages/api-client/  ts-proto bindings + promisified clients
config/                        rendered per-environment configs (edit the Go, not these)
docs/DESIGN.md                 the design and build order
```

## Configuration

The CLI's persistent flags, each defaulting from an environment variable:

| Flag             | Environment variable   | Default | Values                           |
| ---------------- | ---------------------- | ------- | -------------------------------- |
| `--log-level`    | `FIJO_LOG_LEVEL`       | `info`  | `debug`, `info`, `warn`, `error` |
| `--service-name` | `FIJO_SERVICE_NAME`    | `fijo`  | any string                       |
| `--config`       | `FIJO_CONFIG_FILEPATH` | unset   | path to a JSON config file       |

With `--config` set, that file is loaded and `FIJO_`-prefixed variables are
overlaid on top of it. Every variable that can override the config is a constant
in `internal/config/envvars`, regenerated by `make envvars`. Production secrets
arrive that way: `FIJO_DATABASE_WRITE_CONNECTION_PASSWORD`,
`FIJO_DATABASE_READ_CONNECTION_PASSWORD`, `FIJO_TOKENS_SIGNING_KEY` and
`FIJO_EMAIL_RESEND_API_TOKEN` replace the `REPLACE_AT_DEPLOY` markers in
`config/production.json`.

The frontend reads `GRPC_API_SERVER_URL`, `GRPC_INSECURE`,
`COOKIE_ENCRYPTION_KEY` and `COOKIE_NAME`; see `frontend/owner/.env.example`.
