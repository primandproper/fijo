# Fijo: first-pass design

Written 2026-09-21 from the kickoff interview. Decisions here are settled unless a
later section of this file says otherwise. The interview brief is summarized in
the "Settled facts" section; the rest is derived from it.

## Settled facts (from the brief and interview)

- Scheduling for one- and two-person recurring-appointment businesses. The model
  is the weekly grid: standing appointments with exceptions, cancellations and
  make-ups. Nothing music-specific in the core.
- Owners are identity users. Clients are not users: a name and a way to reach
  them, never a password. ~~The client's door is SMS. Email is for owners.~~
  **Revised 2026-11-07:** a contact has an email, a phone, or both, and the
  client's door is email first. See "Email first, and leaving Acuity" below.
- Guardian and attendee are distinct. Anyone on a household with a way to be
  reached who has not opted out gets the reminders. Language is per person.
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
- SMS (deferred to "later" on 2026-11-07, the decision below stands for when it
  returns) is Twilio, and Fijo is the **ISV** on one Twilio account: each studio is
  registered as its own A2P 10DLC **Sole Proprietor** brand through Twilio's ISV
  API, so a studio sends under its own brand from its own number. Per-tenant
  numbers are therefore required rather than deferred. Evaluation until the
  first paying studio runs on verified recipient numbers. See "Messaging
  vendors" below.
- Google Calendar: read-only push with a sync button, after v1.
- platform-go v15 is frozen. Platform changes are additive or filed as issues.

## Email first, and leaving Acuity (decided 2026-11-07)

The first studio's owner was asked what would stop him switching. Two answers,
and they revise the plan:

1. **Switching must ask nothing of his clients.** He wants his existing
   relationships brought over from Acuity, and his plan has no API access
   (Acuity's API is its top tier). Every Acuity plan exports CSV.
2. **Email reaches his clients better than texts do.**

The two meet: a texting number new to the client needs documented opt-in
under 10DLC, and whether consent collected under Acuity carries to a new
sender on a new number is doubtful. A transactional reminder by email needs
neither a carrier registration nor an opt-in round trip, so email is what
makes "the clients do nothing" true on cutover day.

What changes:

- **A contact has an email, a phone, or both**, and at least one. Both are
  unique among a studio's live contacts: the phone because an inbound text is
  filed by its number, the email because the import groups Acuity records by
  it and a re-import matches on it. Emails are stored trimmed and lowercased.
- **Reminders go by email first.** Phase 3 is "the reminder", not "the reminder
  text": timers, bilingual templates, the outbox, behind a channel so SMS can
  join later. Mail is sent from a Fijo domain under the studio's name with
  `Reply-To` set to the owner's own address, so a reply lands in his inbox. No
  per-studio domain, no inbound mail parsing, no Fijo-side thread in v1.
- **SMS, Twilio, the ISV registration and per-tenant numbers move to "later"**,
  together with the inbound thread. The vendor decision below still stands for
  when they return; its open question (an ISV profile for a sole proprietor)
  stops blocking anything.
- **The client page's door (phase 4) is an emailed sign-in link**
  (`signin/magiclinks`), with the phone code for a contact who has only a phone
  once SMS exists.
- **Importing from Acuity is the cutover blocker** and comes before reminders.
  The owner uploads Acuity's two exports, the client list (name, email, phone,
  notes) and the appointments report (time, client, type, price, notes), and
  Fijo shows a preview that writes nothing until he confirms:
  - clients become households and contacts, with records that share an email,
    phone or last name proposed as one household (Acuity has one flat record
    per name booked, so a parent booking two children is two records with one
    email);
  - future appointments grouped by client, type, weekday and time, at one- or
    two-week spacing, become proposed standing lessons from their first future
    date, and what fits no pattern is listed for him;
  - it can be run again: a trial run now and a final one on cutover day, when
    he turns off Acuity's booking and reminders, matching on the email (or the
    phone) rather than writing duplicates;
  - package balances wait for the money phase and are typed in meanwhile.

  Built against a real export from the first studio, not guessed columns.

### The Acuity import as built, first pass (2026-11-08)

A rough cut is in the tree, written before any real export was in hand.
Acuity's help center refuses automated reads, so the headers are a guess
assembled from its prose ("client first name", "start time and date",
"appointment type") and the shorter names third-party guides quote ("First
Name", "Start Time", "Type"). **When the first studio's files arrive, the work
is the alias table in `internal/importing/acuity/headers.go` and the date
layouts in `parse.go`**; the preview's "How the files were read" lists which
header was read as what, and every column not read is named in its warnings
(an intake form's questions turn up there).

- **`internal/importing`** is source-neutral: a `Plan` (households with their
  contacts and attendees, offerings, standing lessons, leftovers, warnings)
  and a `Writer` that marks what is already on the roster (`Annotate`: any
  planned email or phone on a live contact) and writes the rest on one
  transaction (`Write`), materializing each new series as a hand-made one is.
- **`internal/importing/acuity`** turns the two CSVs into a plan, by the rules
  in its package comment: people are their names; records sharing an email or
  phone are one household; whoever has an appointment ahead is a student; a
  reach one person alone carries makes them a contact, and a shared one goes
  to somebody who takes no lessons or to a "<household> family" contact
  flagged as a guessed name; one person's upcoming appointments of one type
  on one weekday and time, weekly or fortnightly, are a standing lesson from
  the first of them, with a warning for each week Acuity has none.
  Appointment types become `single` offerings (Spanish name = English until
  the owner translates), matched by name to what the studio already sells; a
  lesson priced unlike its offering gets the price as its override.
- **Times are read as wall clocks** in the studio's zone; an offset on a value
  is ignored. Past appointments are read only for people; canceled ones and
  duplicates of an appointment ID are dropped.
- **`fijo.v1.ImportService`** has `PreviewAcuityImport` and
  `ApplyAcuityImport`. Nothing is stored between them: apply sends the same
  files and the keys the owner unticked, and plan keys are derived from the
  content so they come out the same. Clients with nothing ahead are shown but
  unticked. The owner who runs the import teaches every lesson it writes.
- **The owner app** has `/clients/import`, linked from `/clients`: two file
  inputs and a language, then the preview with a box per client and per
  lesson, then the counts.
- **Known gaps:** two different people with one name are one person; a re-run
  skips a client already on the roster entirely, so a lesson added in Acuity
  after the first run for an existing client is not picked up; one-off
  appointments are listed rather than written (there is no one-off RPC);
  package balances wait for the money phase; nothing here is audited, as
  roster writes are not yet.

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
- **Contact**: a person with an email, a phone number (E.164), or both. Name,
  language, opted_out, verified_at, relationship (`guardian` or `self`).
  Contacts receive the reminders. The
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

- *(2026-11-07: v1 is outbound email with replies to the owner's inbox; the
  thread and inbound arrive with SMS, "later".)*
- **Thread**: one per household. **Message**: direction, channel (`email`, `sms`),
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

### primitives-go (v2.18.0)

| Package | Used for | When |
|---|---|---|
| `observability`, `config`, `version` | template's bootstrap | v1 |
| `database` + `postgres`, `migrate`, `ddl` | the one database | v1 |
| `server/grpc`, `server/http`, `routing`, `healthcheck` | API, webhook receipt, probes | v1 |
| `authentication` (argon2, tokens), `authentication/webauthn` | owner sign-in engines | v1 |
| `authorization`, `tenancy`, `errors`, `filtering`, `clock`, `identifiers`, `random` | cross-cutting | v1 |
| `email` (provider as DDB) | owner password reset, invitations, client reminders | v1 |
| `secrets/env` | localdev secrets | v1 |
| `webhooks/inbound` | Twilio inbound (new verifier), Stripe callbacks | v1 / money |
| `capitalism/stripe` | checkout and subscriptions | money |
| `sms` | interface, config, noop, mock, twilio | later (was phase 3) |
| `messagequeue`, `jobs` | not needed while timers + outbox + in-process workers suffice | later |

### platform-go (v15.1.0)

| Package | Used for | When |
|---|---|---|
| `identity` (+grpc), `callers`, `rbac`, `sessions` | studios, owners, staff, roles | v1 |
| `authentication/signin` (+grpc), `signin/refreshtokens`, `passwordreset`, `passkeys`, `webauthnsessions` | owner sign-in, password + passkey | v1 |
| `links` | household invites, client action links | v1 |
| `audit` (+grpc) | who moved what | v1 |
| `outbox` | after-commit sends (texts, emails) | v1 |
| `timers` | reminder firing, once fleet-wide | v1 |
| `metering` | email (and later SMS) count per studio | v1 |
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
| ~~SMS sending (primitives-go#20)~~ | primitives-go | `sms` like `email`: `Sender` (`SendSMS` returning a `Receipt` carrying the provider message ID), `OutboundSMS`, config, noop, mock, twilio, and `ErrRecipientOptedOut` | **closed**: built in #25, released in v2.6.0, pinned here |
| ~~Phone verification codes (platform-go#870)~~ | platform-go | store like `signin/magiclinks`: digest at rest, single use, expiry, sweeper | **closed**: `authentication/phonecodes`, in v15.1.0, which Fijo pins |
| ~~Twilio inbound verifier (primitives-go#21)~~ | primitives-go | `webhooks/inbound/twilio`, X-Twilio-Signature over the URL and sorted form params rather than the body | **closed**: built in #23, released in v2.6.0, pinned here |
| Recurrence / series materialization (platform-go#871, candidate) | platform-go | Fijo-local in `schedule`, extraction candidate | file issue, build locally, mark |
| Third-party OAuth grant store (platform-go#872) | platform-go | per-tenant grant with refresh token, encrypted | file issue; Stripe (money) and calendar phases. Not needed for Twilio: the ISV model keeps one account and stores registration state, not per-studio credentials |
| Recovery codes (#867), idempotent refresh (#869) | platform-go | already filed | Jeffrey, in parallel |
| Sign-in door for a proven principal (platform-go#874) | platform-go | `signin.Service.IssueForPrincipal`; passkey logins mint through it | filed in phase 0; `internal/auth/mint` is the candidate |
| Tenant scope on `service.Transports` (platform-go#875) | platform-go | a `TenantScope` resolver beside `Extractor`, or `callers.Principal.TenantScope()` | filed in phase 0; audit read surface inert until then |
| ~~Reply-To on outbound email (found 2026-11-08)~~ | primitives-go | `ReplyToAddress`/`ReplyToName` on `email.OutboundEmailMessage`, honoured by each provider | **closed**: built in primitives-go#78, released in v2.18.0, pinned here |

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
   phase 3 where the sms package exists). *2026-11-07:* contacts gain an
   email, then the **Acuity import** (see "Email first, and leaving Acuity").
3. **The reminder.** ~~`sms` package (noop + Twilio).~~ Reminder scheduling on
   timers, bilingual templates, outbox for after-commit sends, sent by email
   with `Reply-To` the owner, metered. ~~Inbound webhook → thread. Owner thread
   UI with reply. Phone verification store and the invite claim.~~ (moved to
   later with SMS)
4. **Client page.** Emailed sign-in link, upcoming lessons, balance, "ask to
   reschedule" mails the owner.
5. **Money.** Offerings ↔ billing products, packages as purchases feeding the
   balance ledger, recurring as subscriptions with price override, Stripe
   Checkout, `billing/sync` on the Stripe webhook.
6. **Later.** Waitlist, dataprivacy and retention flows, entitlements gating,
   Google Calendar push (OAuth grant store gap). SMS (2026-11-07): the `sms`
   package, Twilio as ISV with per-tenant numbers and A2P registration, inbound
   webhook → thread with the owner's thread UI, phone verification and the
   invite claim.

## Phase 1 as built (2026-09-22)

The grid is in the tree: the schema, the store, the materializer, the audited
commands and the gRPC surface, driven end to end against a running API.

### What it is

`internal/domain/schedule` holds seven tables and the logic over them.
Offerings are what a studio sells, series are the standing slots, occurrences
are the appointments those slots produce, and closures are the days the studio
is shut. `Date` and `ClockTime` are civil types with no zone, because a series
is a rule — Tuesdays at four — and only an occurrence is an instant.

Attendees and the balance ledger are absent. Both need the roster, which is
phase 2, and nothing here assumes their absence beyond not naming them.

### Three decisions the schema made

- **Durations and wall clocks are integers.** An INTERVAL does not cross
  `database/sql` without a custom type and a TIME column arrives as a string,
  which a container test found before any of this shipped. Minutes and days.
- **A series stores a rule, not instants.** Weekday, minute of day, civil
  start date. The instant is computed in the studio's zone when the row is
  materialized, which is the only place daylight saving is resolved. Both
  awkward wall clocks a year are documented at `Date.At` and pinned by tests:
  Go resolves a skipped hour backwards, and an ambiguous one to the first of
  the two.
- **Materialization is idempotent** by a partial unique index on
  `(series_id, starts_at)`, so the worker can run on a cadence and a series
  edit can re-run it immediately.

### Taking sqlc

The schedule's first table was the point to decide, and sqlc came with it.
`sqlc.yaml` renders a querier per domain package as DDB's does, with
`emit_methods_with_db_argument` so the platform's convention survives codegen:
a generated method takes its executor, so a write is handed a `Tx` and a read
a reader. sqlc reads Fijo's half of the migration sequence; the platform's half
is rendered at runtime, so a query here cannot join `identity_users` — the
boundary the tier rule already draws. `owners` was converted as the pilot.

### The audit read, and why it is not what phase 1 planned

Phase 0 recorded that the audit read surface is inert because
`service.Transports` derives audit's scope from `Principal.Scope()`, which is
the global directory here (platform-go#875), and proposed hand-mounting audit
the DDB way in phase 1.

**That is not available.** `service.Transports` has no opt-out: audit mounts
whenever an `audit.Reader` is in the injector, and `auditcfg.RegisterReader`
puts one there whenever the audit config block is present — which it must be,
because the same block registers the recorder the commands write through.
Hand-mounting would mean abandoning `service.RegisterTransports` for every
surface and losing the "config presence is composition" property the whole
composition root is built on.

So the grid's history is **Fijo's own RPC**: `ScheduleService.ListHistory`
reads `audit.Reader` in the caller's studio scope, which is the scope the
commands recorded under. The platform's mounted surface stays where it was —
correct for the operator's global entries, blind to a studio's — and #875
remains filed. Fijo's read is the better shape for the product anyway: an
owner's history is filtered by occurrence and series, not by the generic
console's fields.

### The command layer

Every act that changes the grid is a `schedule.Commands` method: one
transaction holding the rows and the audit entry, because an entry that can be
missing describes a history that can lie. The store knows how to change a row
and refuses an illegal transition; the command layer knows who asked, why, and
what else changes with it — ending a series cancels what stands after its last
day, closing the studio cancels what stands inside the range, a move writes a
new row and leaves the old one naming it, and whether a cancellation was late
is judged against the notice window at that moment and then kept.

The caller is an argument rather than something read off the context, so a
layer that is called from somewhere without a principal cannot fail to notice.

### Open for phase 2

- ~~**The roster** brings attendees.~~ Done (2026-10-09), see below. The
  payer the balance ledger needs is still open.
- ~~**Materializing on a series edit.**~~ Done (2026-09-23): creating or
  updating a series or an offering runs `Materializer.SyncSeries` on the same
  transaction, which retracts (archives) standing rows from now on that the
  rule no longer produces and writes what it now does; anything an owner has
  acted on is left alone. Settings and reopening run `SyncStudio`.
- ~~**The owner app** has no grid yet.~~ Done (2026-09-23): `/week` is the
  week grid in the owner's zone with every occurrence command and its history,
  `/offerings` and `/series` set up what the grid holds, and `/studio` holds
  the notice window, the horizon and closures. The account page moved to
  `/account`. Not yet in the UI: reassigning staff (one-person studios don't
  need it) and one-off appointments, which have no RPC.

## Phase 2 as built, first half (2026-10-09)

The roster is in the tree: households, contacts and attendees, and every
standing slot is now somebody's.

- **`internal/domain/roster`** holds the three tables (`00022_roster.sql`).
  A household has a name, a language and notes. A contact has a name, an E.164
  phone, a language and a relationship (`guardian` or `self`). An attendee has
  a name, a language and an optional `contact_id`, which is how an adult
  student is one person rather than two. Reminder offsets wait for phase 3,
  which is the first thing that reads them. `opted_out` and `verified_at` exist
  but nothing writes them yet.
- **A phone is unique among a studio's live contacts**, because an inbound text
  will be filed onto a household by the number it came from. The same number
  in two studios is fine. `NormalizePhone` drops punctuation and reads a number
  with no `+` as North American when it has ten digits, or eleven starting
  with 1; anything else must give its country code.
- **Every series names at least one attendee.** `Series.Validate` refuses an
  empty set, and the store writes the join through an `INSERT ... SELECT` from
  `roster_attendees` under the slot's studio, so an attendee from another
  studio, or an archived one, is refused rather than written
  (`00023_schedule_attendees.sql`). Occurrences carry their own attendee set,
  copied from the series when materialized and from the original on a move or
  a make-up. `SyncSeries` gives a standing row the series' current attendees in
  place, keeping its identifier, so adding a sibling to a slot adds them to
  every lesson still ahead.
- **Nobody with a standing slot is archived.** The roster's statements cannot
  see the schedule's tables, so `RosterService` asks
  `schedule.Store.LiveSeriesForAttendees` before archiving an attendee or a
  household, and refuses with `FailedPrecondition`.
- **`fijo.v1.RosterService`** is CRUD over the three nouns, every method public
  to the enforcer and scoped by the principal like the schedule's.
  `CreateHousehold` takes the household's first contacts and attendees and
  writes them on one transaction; a contact with `attends` is also written as
  their own attendee.
- **The owner app** has `/clients` (search, and "add a client" in one form) and
  `/clients/[id]` (the household's people, each student's standing lessons, and
  adding a standing lesson from the student). `/series` is now "Lessons" and
  leads with who each one is for, and the week view names the students.
- **devseed** adds three sample households with four standing lessons and
  materializes the grid.

Not done yet in phase 2: the payer on a series, editing attendees on a single
occurrence (the store has `SetOccurrenceAttendees`; there is no RPC), the
invite link, and roster audit entries (roster writes record nothing today).

## Phase 3 as built, first pass: the email reminder (2026-11-08)

Reminders go by email, in each contact's language, from the studio's name.

- **When one is owed is never stored ahead of time.** Lessons are cancelled,
  moved, re-timed and handed to new students by many writes, and a reminder
  scheduled at booking would have to be found and moved by each of them.
  Instead a planner in `fijo worker` asks once a minute which reminders are
  owed now (`messaging.Store.Candidates`, one SQL read over the week ahead,
  and `Candidate.Owed`), reading the lessons as they are. This departs from
  the domain model's "keyed into timers at booking"; timers are still used,
  for delivery.
- **What is stored is a reminder once it is owed** (`messaging_reminders`,
  `00027_reminders.sql`): the words as rendered, and a unique key on (lesson,
  contact, offset) that makes it go once across the fleet. It is written on
  the same transaction as a platform **timer** (`fijo_reminders` set) that
  delivers it: the timer's lease, retry and at-least-once firing are the
  outbox, so Fijo writes none. Delivery holds the row while it calls the
  provider, re-checks that the reminder is still owed (lesson still
  scheduled and not started, contact and studio still on) and marks it
  skipped if not, gives up after five provider refusals, and counts each
  sent email on the platform's **metering** (`reminder_emails`, per studio
  per month) in the transaction that marks it sent.
- **Which reminder is owed** is the latest-firing one that has come due, so a
  worker down for a day sends the hour-before and not both. None is owed whose
  instant was before reminders were turned on (so turning them on is not a
  burst for the day already under way) or before the lesson was booked (a
  lesson booked this morning gets its hour-before, not a late day-before).
- **Reminders are off until the owner turns them on**, on the new Reminders
  page, with a reply address that defaults to their own. Two systems sending
  reminders on cutover day is the failure to avoid. "Send me a test" mails a
  sample, in either language, to that address.
- **Per household**, two reminder times (a day and an hour before by
  default, each anything from 15 minutes to a week, or none); **per contact**,
  an owner-set `reminders_off`, kept apart from the SMS-era `opted_out`. Both
  are separate RPCs (`SetHouseholdReminders`, `SetContactReminders`) so the
  existing edit forms cannot reset them.
- **A reply lands in the owner's inbox**: every reminder carries Reply-To
  the studio's reply address, under the studio's name (primitives-go#78,
  v2.18.0, closed the gap the first pass found). The body names the address
  too, for a client who writes fresh rather than replying.
- **Migrations**: the timer and metering tables are platform DDL spliced in
  at versions 25 and 26. Numbering them 9 and 10, inside the old platform
  block, was refused by a database already at 24 as out of order; a platform
  table added later takes the next free number.

Not done in this pass: an SMS channel; a reminder's
lesson changing time in place after its reminder went (the unique key means
the same offset is not sent again for the new time); and bounce or complaint
handling from the provider.

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

### How a studio comes to exist (decided 2026-09-22, revised 2026-10-09)

Studios **sign themselves up**. platform-go v15 made sign-in's `Register` the
one registration RPC, anonymous and open unless a deployment closes it, and
Fijo leaves it open: `serve` mounts `SignInService` because the composition
root builds a `signin.Service` with identity as its registrar, and a
registration mints a user, a studio and an `owner` membership (the default
owner roles `NewService` is handed). The original decision deferred
self-service to phase 5, with a Stripe Checkout behind the sign-up page; the
door now opens before the checkout does, and phase 5 puts the checkout in
front of it.

What is not built yet is the owner app's sign-up page; until it is, the door is
reachable from `grpcurl` and not from the app. The page has to send a time
zone — the browser's — because the materializer refuses a studio whose account
names none (`studios.ErrNoTimeZone`), where `internal/bootstrap` falls back to
`DefaultTimeZone`.

One act has no door on an empty database: granting `service_admin`, which is a
`SetUserServiceRoles` call needing a permission nobody holds yet. It lives in
`internal/bootstrap` beside registering a studio directly — the same
`signin.Service.Register` the wire calls, without the server — and that package
is deliberately unreachable over the wire and authorized by the database
credential its caller already had to hold. Three callers share it:
`cmd/tools/bootstrap` (the operator's, run once against a deployment, safe to
run again), `cmd/tools/devseed` (the developer's, local) and container tests
through `internal/testutils/seed`, because a test of anything scoped to a
studio needs a studio before it can begin. The shape follows DDB's
`cmd/tools/bootstrap`: flags rather than environment variables, idempotent by
username, the password hashed at boot rather than baked into a migration, and
the promotion a second write after the registration.

**An operator can provision a studio too**, through the same `Register` call
made on somebody's behalf. The studio CRUD an operator needs after that is
identity's account surface, which `serve` already mounts and `service_admin`
already holds the permissions for (`CreateAccounts`, `ListAllAccounts`,
`ArchiveAccounts`, `TransferAccountOwnership`, and `UpdateAccounts` by
inheriting `owner`). Note that `CreateAccount` mints the account for the
*calling* principal, so provisioning somebody else's studio is `Register`, not
`CreateAccount`.

The second registration door is the **invitation-answering**
one, in phase 2, when a two-person studio's second person needs to sign in:
`signin.Registration` already carries `InvitationID` and `InvitationToken`, no
account is minted, and the token is the authorization rather than the door
being open.


### Messaging vendors (decided 2026-09-22)

*SMS moved to "later" on 2026-11-07 (see "Email first, and leaving Acuity");
what follows is the decision for when it returns.*

**Twilio, with Fijo as the ISV.** 10DLC is a carrier requirement enforced
through The Campaign Registry, so no provider avoids it and changing vendors
changes nothing about the registration. What varies is the onboarding model,
and the ISV one is the reason this is tolerable for the customer: Fijo holds
one Twilio account with an approved ISV Reseller profile and registers each
studio's brand and campaign through Twilio's API.

What a studio does to get texting is fill in a Fijo form — name, email,
contact phone, a US or Canadian mobile, address, business name — and reply to
one OTP text sent to that mobile. No Twilio account, no console, no EIN. The
Sole Proprietor brand type exists precisely for a person without a tax ID,
which a one-person studio generally is; a studio that *has* an EIN registers
as Low-Volume Standard instead, which is the same flow with better throughput.

Sole Proprietor limits are one campaign, one number, about one message per
second, roughly 1,000/day on T-Mobile and around 15/minute on AT&T. A studio
of sixty students on two reminders each sends about 240 texts a week, so the
ceiling is not a consideration until a studio is far larger than the product
is for.

Consequences:

- **Per-tenant numbers are phase 3, not "later."** A sole proprietor brand
  carries one number, so the studio's number is part of its registration.
  Inbound matching files on the number the text arrived at as well as the
  sender.
- **The messaging domain grows registration state** per studio: brand ID,
  campaign ID, number, and where in the OTP round trip it is. Not credentials
  — Fijo holds the only Twilio account.
- **Each studio is the sender of record for its own content**, which is the
  right side of that line to be on.
- **This leans hosted.** A self-hosted deployment would have to be its own ISV
  or register directly, so the ISV model and the single-tenant story do not
  compose. That is consistent with the studio provisioning decision above.
- **Toll-free is not the escape hatch for studios** it used to be: since
  February 17, 2026 verification requires a business registration number for
  every business type except a sole proprietorship with no tax ID. That
  exception is what makes a toll-free number verified as `SOLE_PROPRIETOR` the
  cheapest way to text a handful of real people during development, without
  an EIN or the ISV profile. It still needs an upgraded account and a public
  privacy policy and terms (required since September 15, 2026).
- **The Twilio trial cannot develop against.** It sends only Twilio's own
  templates, to at most five verified numbers, for thirty days. Phase 3 is
  built on the noop sender with a dev-only simulated phone instead.

Open: whether Twilio will approve an ISV Reseller profile for a US sole
proprietor with no EIN. To be confirmed with Twilio (and with Telnyx, whose
equivalent ISV programme prices lower and which the model ports to unchanged)
before phase 3 starts. If the answer is no, an EIN is a free same-day IRS
application and does not require forming an entity.

### Open for phase 1

- **Email in localdev** is the noop provider, so a reset link is visible only in
  the debug log. primitives-go has no SMTP provider and its email mock is a moq
  mock rather than a config-selectable one, so a local sink is an additive
  primitives-go provider rather than a config change — a small piece of work in
  a tier that is not frozen, whenever it is worth doing.
- **Rendered `production.json` carries `REPLACE_AT_DEPLOY` markers** for the
  database password, the signing key and the Resend token; the deployment
  supplies them through `FIJO_*` variables (the envvars package names them).
