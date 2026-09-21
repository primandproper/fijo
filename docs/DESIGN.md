# Fijo: first-pass design

Written 2026-09-21 from the kickoff interview. Decisions here are settled unless a
later section of this file says otherwise. The interview brief is summarized in
the "Settled facts" section; the rest is derived from it.

## Settled facts (from the brief and interview)

- Scheduling for one- and two-person recurring-appointment businesses. The model
  is the weekly grid: standing appointments with exceptions, cancellations and
  make-ups. Nothing music-specific in the core.
- Owners are identity users. Clients are not users: a phone number and a name,
  never a password. The client's door is SMS. Email is for owners.
- Guardian and attendee are distinct. Anyone on a household with a phone number
  who has not opted out gets the texts. Language is per person.
- Every client-facing string ships in English and Spanish. Not to be re-litigated.
- An appointment is a service, a time, one or more attendees, one or more staff.
  Staff is reassignable per occurrence. Group appointments exist.
- Offerings: per-lesson, packages ("5 for $250", one balance, optionally shared
  across attendees), and recurring arrangements ("until otherwise noted", flat
  monthly). One advertised price, override on every agreement.
- Balances move automatically (completion, no-show, late cancel) and the provider
  can revert any transition.
- Reminders: per-household configurable offsets, default day-before and
  hour-before, per-contact opt-out. Replies land in a human-read thread; nothing
  parses keywords in v1. No reply means nothing happens.
- Web only. SvelteKit server talks gRPC to the Go API, as DDB does. Owner sign-in
  is password plus passkey from day one (platform-go #867 lands in parallel).
- Postgres only: timers, workqueue and operations are Postgres-only by decision.
- Time zone stored per user and per studio from day one. First tenant is
  US/Central.
- Stripe only, via Stripe Checkout. Metering of texts from day one; plan gating
  and Fijo's own pricing deferred.
- No SMS brand registration (10DLC, toll-free verification) yet. Evaluation runs
  on a Twilio account with verified recipient numbers. Per-tenant numbers deferred.
- Google Calendar: read-only push with a sync button, after v1.
- platform-go v14 is frozen. Platform changes are additive or filed as issues.

## Tenancy and roles

The studio is an identity account and is Fijo's tenant: every Fijo row carries
`tenancy.Scope` = the studio account. Owners and staff are account members with
rbac roles `owner` and `staff`. Jeffrey is `service_admin`.

Households are not accounts and contacts are not users. The interview floated
"guardian = account admin, attendee = account member"; that contradicts the
settled fact that clients never become users, so it is not the model. Households
live in Fijo's own roster domain, scoped to the studio.

## Domain model

Three Fijo-owned domains, each a Store plus Postgres SQL plus mock, DDB-style.

### roster

- **Household**: the client account. Default language, reminder offsets, notes.
- **Contact**: a person with a phone number (E.164). Name, language, opted_out,
  verified_at, relationship (`guardian` or `self`). Contacts receive texts. The
  payer on any agreement is a contact.
- **Attendee**: a person who attends. Name, language, optional `contact_id` when
  they carry their own phone. An adult who attends is one person that is both a
  contact and an attendee. A child is an attendee with no contact.
- Guardianship is implicit: every contact on a household is treated as a guardian
  of every attendee on it. An explicit guardian table is deferred until a case
  (separated parents with different children) needs it.
- **Invite**: an action link (`links`) that lets a contact claim a household by
  verifying the phone (the phone-verification gap).
- The roster is Fijo's domain and stays here. A household, its attendees and
  their reminder preferences are a scheduling business's nouns, not every
  product's. The generic residue is the phone's lifecycle: verification is the
  platform-go code store, and opt-out is a carrier fact the `sms` package
  surfaces as a sentinel Fijo records on the contact.

### schedule

- **Offering**: name (bilingual), duration, list price, kind:
  `single`, `package` (lesson count, optional expiry, shareable), `recurring`
  (monthly price).
- **Series** (the standing slot): offering, weekday, start time in studio time,
  interval in weeks (1 or 2), staff set, attendee set, start date, optional end
  date, price override, linked agreement.
- **Occurrence**: one row per instance, materialized from the series out to a
  horizon (8 weeks) by a worker and on series edits. Carries its own staff and
  attendee sets (so a cover teacher is a per-occurrence edit), a state, a
  `replaces_occurrence_id` for make-ups, and a reason. One-off appointments are
  occurrences with no series. Materialized rows are what reminders, threads,
  balances and the audit trail point at; the series is never the thing that
  fires.
- **Occurrence states**: `scheduled` → `completed` | `cancelled` | `no_show` |
  `moved`. `cancelled` records who (household, studio, closure) and whether it
  was inside the notice window. `moved` points at the replacement.
- **Commands**, each its own audited action with its own text audience:
  cancel one occurrence; end a series from a date; close the studio for a date
  range; reassign staff on an occurrence; move an occurrence; schedule a make-up
  against a cancelled occurrence; mark completed / no-show; revert.
- **Balance ledger**: entries per package purchase, +N on purchase, −1 on
  completion, no-show or late cancel, +1 on revert, with the occurrence as the
  reason. Balance is a sum. A shareable package is one ledger drawn on by any
  attendee on the household.
- **Notice window**: per-studio setting; "late" is computed against it.
- **Recurrence** is weekly-with-interval only. No RRULE. This is the recurrence
  gap: what Fijo needs is series materialization with exceptions, a noun with a
  table and therefore platform-go's tier, but generalizing from one consumer is
  premature. It lives in `internal/domain/schedule` marked as an extraction
  candidate, with an issue describing the shape.

### messaging

- **Thread**: one per household. **Message**: direction, channel (`sms`),
  body, language, provider message id, status, optional occurrence id, optional
  reminder id.
- **Reminder**: a scheduled outbound text for (occurrence, contact, offset),
  keyed into `timers` so it fires once fleet-wide. Rescheduling or cancelling an
  occurrence cancels its timers and reschedules. Templates are bilingual and
  rendered in the contact's language.
- **Inbound**: a carrier webhook (Twilio) received through `webhooks/inbound`
  with a Twilio verifier (a gap), enqueued, filed onto the household's thread by
  matching the sender number to a contact. Unknown numbers file onto a studio
  "unmatched" thread.
- Every text sent is metered (`metering`) against the studio.

## What Fijo composes

"v1" is phases 0–4 below; "money" is phase 5; "later" is phase 6.

### primitives-go (v2.5.0)

| Package | Used for | When |
|---|---|---|
| `observability`, `config`, `version` | template's bootstrap | v1 |
| `database` + `postgres`, `migrate`, `ddl` | the one database | v1 |
| `server/grpc`, `server/http`, `routing`, `healthcheck` | API, webhook receipt, probes | v1 |
| `authentication` (argon2, tokens), `authentication/webauthn` | owner sign-in engines | v1 |
| `authorization`, `tenancy`, `errors`, `filtering`, `clock`, `identifiers`, `random` | cross-cutting | v1 |
| `email` (provider as DDB) | owner password reset, invitations | v1 |
| `secrets/env` | localdev secrets | v1 |
| `webhooks/inbound` | Twilio inbound (new verifier), Stripe callbacks | v1 / money |
| `capitalism/stripe` | checkout and subscriptions | money |
| **`sms` (gap)** | interface, config, noop, mock, twilio | v1, phase 3 |
| `messagequeue`, `jobs` | not needed while timers + outbox + in-process workers suffice | later |

### platform-go (v14.0.0)

| Package | Used for | When |
|---|---|---|
| `identity` (+grpc), `callers`, `rbac`, `sessions` | studios, owners, staff, roles | v1 |
| `authentication/signin` (+grpc), `signin/refreshtokens`, `passwordreset`, `passkeys`, `webauthnsessions` | owner sign-in, password + passkey | v1 |
| `links` | household invites, client action links | v1 |
| `audit` (+grpc) | who moved what | v1 |
| `outbox` | after-commit sends (texts, emails) | v1 |
| `timers` | reminder firing, once fleet-wide | v1 |
| `metering` | SMS count per studio | v1 |
| `service`, `errormappers`, `privacyadapters` | composition root | v1 |
| `billing`, `billing/sync`, `billing/plans`, `billing/standing` | offerings as products, packages as purchases, recurring as subscriptions | money |
| `entitlements` | seat and SMS-allowance gating | later |
| `dataprivacy`, `retention`, `shredding` | privacy adapters are written with each store; the flow lands later | later |
| `waitlists` | a slot that opens up | later |
| `settings` | not used; household and studio preferences are Fijo columns | no |
| `notifications`, `notifications/push` | web-only, no push in v1; #866 is not on the critical path | no |
| `webhooks` (outbound), `comments`, `issuereports`, `saga`, `operations`, `workqueue`, `mediaregistry`, `searchsync` | no consumer need | no |

### Gaps, and where each is met

| Gap | Tier | Shape | Action |
|---|---|---|---|
| SMS sending (primitives-go#20) | primitives-go | `sms` like `email`: `Sender` interface, `OutboundSMS`, config, noop, mock, twilio, and a sentinel for a recipient who opted out | build additive in primitives-go, release v2.6 |
| Phone verification codes (platform-go#870) | platform-go | store like `signin/magiclinks`: digest at rest, single use, expiry, sweeper | build additive in platform-go v14.x |
| Twilio inbound verifier (primitives-go#21) | primitives-go | `webhooks/inbound/twilio`, X-Twilio-Signature | build additive |
| Recurrence / series materialization (platform-go#871, candidate) | platform-go | Fijo-local in `schedule`, extraction candidate | file issue, build locally, mark |
| Third-party OAuth grant store (platform-go#872) | platform-go | per-tenant grant with refresh token, encrypted | file issue; calendar phase |
| Recovery codes (#867), idempotent refresh (#869) | platform-go | already filed | Jeffrey, in parallel |
| Sign-in door for a proven principal (platform-go#874) | platform-go | `signin.Service.IssueForPrincipal`; passkey logins mint through it | filed in phase 0; `internal/auth/mint` is the candidate |
| Tenant scope on `service.Transports` (platform-go#875) | platform-go | a `TenantScope` resolver beside `Extractor`, or `callers.Principal.TenantScope()` | filed in phase 0; audit read surface inert until then |

## Build order

0. **Scaffold.** Run `rename.sh github.com/primandproper/fijo`. Add `serve`,
   `worker`, `migrate` subcommands. Postgres, identity, signin with password and
   passkey, sessions, rbac roles, audit. Time zone on user and studio. SvelteKit
   shell with owner sign-in. CI green.
1. **The grid.** Offerings, series, occurrence materialization worker, the
   week view, the commands (cancel one, end series, close studio, reassign,
   move, make-up, complete, no-show, revert). Audit on every command. No texts
   yet.
2. **The roster.** Households, contacts, attendees, languages, opt-outs.
   Attach attendees and payer to series. Invite link (verification deferred to
   phase 3 where the sms package exists).
3. **The reminder text.** `sms` package (noop + Twilio). Reminder scheduling on
   timers, bilingual templates, outbox for after-commit sends, SMS metering.
   Inbound webhook → thread. Owner thread UI with reply. Phone verification
   store and the invite claim.
4. **Client page.** Phone-code sign-in, upcoming lessons, balance, "ask to
   reschedule" opens the thread.
5. **Money.** Offerings ↔ billing products, packages as purchases feeding the
   balance ledger, recurring as subscriptions with price override, Stripe
   Checkout, `billing/sync` on the Stripe webhook.
6. **Later.** Waitlist, dataprivacy and retention flows, entitlements gating,
   Google Calendar push (OAuth grant store gap), per-tenant numbers and A2P
   registration.

## Repo skeleton (derived from fijo)

```
cmd/main/main.go                      thin entrypoint (template)
cmd/tools/codegen/{configs,envvars}   template's generators, grown per config
internal/cli/{root,version,serve,worker,migrate}.go
internal/config/                      template's Config, grown: database, grpc,
                                      http, email, sms, stripe, timers, outbox
internal/build/{api,worker}/          composition roots, DDB-style
internal/domain/roster/               store, postgres (sqlc), mock, privacy
internal/domain/schedule/             store, postgres, mock, privacy, materializer
internal/domain/messaging/            store, postgres, mock, privacy, templates
internal/services/{roster,schedule,messaging}/grpc
internal/workers/{materialize,reminders,outboxrelay,inbound}
proto/fijo/v1/*.proto
migrations/
frontend/                             SvelteKit: owner area, client area,
                                      packages/api-client (grpc-js, as DDB)
config/{localdev,production}.json
docs/
```

## Phase 0 as built (2026-09-21)

Phase 0 is in the tree. What follows records the decisions it took that the
sections above did not anticipate, and the two platform gaps it found.

### Composition

The composition root is platform-go's `service` package driven from Fijo's
config, not DDB's hand-assembled registration. `internal/config.Config` embeds
`service.Config`, so a platform subsystem is switched on by being present in
`config/<env>.json`; `internal/build/api` adds only what no environment
variable can express (hooks, the session store, the sign-in service and its
flows, the interceptors, the authorization requirements) and mounts identity,
sign-in and audit beside Fijo's two gRPC services through
`service.RegisterTransports`. `internal/build/worker` is the same registration
with the servers left out plus the sweepers.

### Sign-in

- **Password** sign-in and the **refresh exchange** are platform-go's own
  `SignInService`, mounted as-is.
- **Passkeys, password reset and sign-out** are `fijo.v1.OwnerAuthService`,
  built over the platform's `passkeys`, `webauthnsessions`, `passwordreset` and
  `refreshtokens` stores and primitives-go's WebAuthn engine.
- **Sessions** are the revocation record signin's documentation leaves to the
  consumer: every mint creates one `sessions` row through the claims builder and
  names it in the token; the interceptor reads it back on every request. One
  row per mint rather than per family, because `sessions.Store` has no lookup
  but by identifier; the rows a refresh leaves behind idle out and are swept.
- **Audit** entries are recorded through `identity.Hooks` and `signin.Hooks`,
  inside the platform's transactions, scoped to the studio
  (`tenancy.Of(accountID)`); an event with no studio is the operator's, in the
  global scope.
- **Roles** are seeded by the migrator from `internal/authz/roles`, and the
  role a membership names is a foreign key onto the seeded table.

### Two platform gaps found in phase 0

| Gap | Tier | Where Fijo stands meanwhile |
|---|---|---|
| **A sign-in door for a principal proven by another credential** (platform-go#874). `signin.Service` proves a password or a mailed link on its way to every token; a passkey login has nowhere in the platform to get the same token, family and hooks. | platform-go, additive: a `signin.Service` method taking a proven principal | `internal/auth/mint` duplicates the mint shape (same claims builder, refresh store and hooks) and is marked as the extraction candidate. |
| **`service.Transports` derives audit's scope from `Principal.Scope()`** (platform-go#875). identity reads that as the directory scope, so it is global here, while Fijo's audit rows are studio-scoped. The auto-mounted audit read surface therefore sees no studio entries. DDB avoids this by hand-mounting audit with its own `ScopeResolver`, which the `service` root does not let a consumer do. | platform-go, additive: a scope-resolver seam on `Transports` (audit's `ScopeResolver`, operations' `OwnerResolver`) | Recording is correct and studio-scoped; the read surface is inert until the seam lands or phase 1 hand-mounts audit the DDB way. |

Both are filed against platform-go as #874 and #875. Neither blocks phase 1.

### Open for phase 1

- **How the first owner comes to exist.** The platform's `Register` RPC needs an
  authenticated registrar, so `cmd/tools/devseed` is the only door on an empty
  database today. Production needs a decision: a self-serve "create your studio"
  page (a public registration door Fijo would have to build over
  `signin.Service.Register`), operator-created studios through an admin
  surface, or both.
- **The operator.** Nothing grants `service_admin` yet; it is a
  `SetUserServiceRoles` call that itself needs the permission. The seed tool or
  a one-time migration is the likely answer.
- **Email in localdev** is the noop provider, so a reset link is visible only in
  the debug log. A local mail sink (or the platform's mock provider) would make
  the reset flow exercisable in the browser.
- **Rendered `production.json` carries `REPLACE_AT_DEPLOY` markers** for the
  database password, the signing key and the Resend token; the deployment
  supplies them through `FIJO_*` variables (the envvars package names them).
