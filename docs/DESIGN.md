# Fijo: what is left to build

This file lists only the work still ahead. What is already built is described by
the code, its package comments and the git history (the design as it stood
before this cleanup is in the history of this file). Each item carries the
decisions already made about it, so none of them has to be settled again.

Standing constraints on everything below: every string a client sees ships in
English and Spanish, in the person's own language; clients are never users (a
name and a way to reach them, never a password); a wall clock is not an instant;
platform-go v15 is frozen, so what Fijo needs from it is an additive change
there or a filed issue.

## 1. Cutover: what stands between the first studio and turning Acuity off

Switching must ask nothing of his clients, so the import and email reminders
have to be right on the day he turns off Acuity's booking and reminders.

- **Read a real export.** The importer's headers are a guess, because
  Acuity's help center refuses automated reads. When the first studio's two
  files (the client list and the appointments report) arrive, correct the alias
  table in `internal/importing/acuity/headers.go` and the date layouts in
  `parse.go`. The preview's "How the files were read" shows which header was
  read as what, and its warnings name every column that was not read.
- **Import gaps:**
  - Two different people who share a name become one person.
  - A re-run matches a client's standing lesson by weekday and time. If Acuity
    has moved a client's standing time since the last run, the new time is
    added and the old lesson stays until the owner ends it. The preview shows
    the new one, so the owner can untick it.
  - An imported one-off doesn't keep Acuity's price, because an occurrence has
    no price of its own until the money phase (§4).
  - Package balances wait for the money phase (§4) and are typed in until then.
- **A deployment.** `deploy/environments/` has only `localdev`. Production
  needs somewhere to run `migrate`, `serve` and `worker`, the
  `REPLACE_AT_DEPLOY` values in `config/production.json` (database password,
  signing key, Resend token) supplied through `FIJO_*` variables, a sending
  domain for Fijo's mail, and one run of `cmd/tools/bootstrap` for the first
  `service_admin`.
- **Bounces and complaints.** Nothing handles them from the email provider.

## 2. The rest of the roster (phase 2)

- **The payer on a series.** The payer on an agreement is a contact on the
  household. The money phase's balance ledger and subscriptions need it.
- **A client's history on screen.** Roster writes are recorded (through the
  store's `RecordingHooks`, names, emails, phones and notes as digests), and
  so are an import's runs and the one-offs it books, but nothing reads them
  back yet. A household's page would read its own entries and those whose
  `household_id` metadata names it.
- **Offerings and series are not audited.** Creating or editing them goes
  straight to the store, by hand or by import; only the grid's commands, the
  roster and an import's one-offs and runs record anything.
- **Attendees on a single occurrence.** The store has
  `SetOccurrenceAttendees`; there is no RPC or UI.
- **Reassigning staff in the UI.** `ReassignOccurrence` exists; the owner app
  doesn't expose it. One-person studios don't need it, so it can wait for the
  second person.
- **The second person in a studio.** Their way in is sign-in's
  invitation-answering registration (`signin.Registration` carries
  `InvitationID` and `InvitationToken`): no account is minted, and the token is
  the authorization. Needs an invite flow in the owner app, and probably the
  first Fijo permission, because Fijo's RPCs are all self-service today.
- **The owner sign-up page.** Sign-in's anonymous `Register` is mounted and
  open, but there is no page. The page must send the browser's time zone,
  because the materializer refuses a studio whose account names none
  (`studios.ErrNoTimeZone`). Once money exists (§4), Stripe Checkout goes in
  front of it.

## 3. Client page (phase 4)

- **The door is an emailed sign-in link** (`signin/magiclinks`) to a contact's
  email. A contact who has only a phone gets a phone code
  (`authentication/phonecodes`) once SMS exists (§5). Open: what the link signs
  a contact into, since clients are not identity users.
- **What it shows:** upcoming lessons for the household's attendees and the
  balance (the balance arrives with §4).
- **"Ask to reschedule"** emails the owner, with Reply-To the contact. Nothing
  changes on the grid until the owner acts.
- Bilingual like everything else a client sees.

## 4. Money (phase 5)

Stripe only, via Stripe Checkout, through platform-go's `billing`,
`billing/sync`, `billing/plans` and `billing/standing`, and primitives-go's
`capitalism/stripe`.

- **Offerings become billing products.** One advertised price, with an
  override on every agreement (the series already carries a price override).
- **Packages** ("5 for $250", optional expiry, optionally shared across a
  household's attendees) are purchases feeding a **balance ledger**: +N on
  purchase; −1 on completion, no-show or late cancel; +1 on revert. Each entry
  names its occurrence as the reason, and the balance is their sum. A
  shareable package is one ledger any attendee on the household draws on.
  Balances move automatically, and the owner can revert any transition.
- **Recurring arrangements** ("until otherwise noted", flat monthly) are
  subscriptions with the price override.
- **`billing/sync`** on the Stripe webhook (`webhooks/inbound`).

## 5. Later (phase 6)

- **SMS.** The decisions below stand for when it returns.
  - **Twilio, with Fijo as the ISV** on one Twilio account. Each studio is
    registered through Twilio's ISV API as its own A2P 10DLC **Sole
    Proprietor** brand (or Low-Volume Standard, if it has an EIN), so a studio
    sends under its own brand from its own number. Per-tenant numbers are
    therefore required. To get texting, a studio fills in a Fijo form (name,
    email, contact phone, a US or Canadian mobile, address, business name) and
    replies to one OTP text: no Twilio account, no console, no EIN.
  - **The messaging domain grows registration state** per studio (brand ID,
    campaign ID, number, where it stands in the OTP round trip), not
    credentials.
  - **The pieces:** primitives-go's `sms` (noop, mock, twilio), a reminder
    channel beside email, metering of texts per studio, and an opt-out from the
    `sms` package's `ErrRecipientOptedOut` sentinel recorded as `opted_out` on
    the contact, which is separate from the owner-set `reminders_off`.
  - **Inbound** arrives through `webhooks/inbound/twilio` and is filed onto the
    household's **thread** by matching the sender's number (and the number it
    was sent to) to a contact. Unknown numbers go to a studio "unmatched"
    thread. Messages record direction, channel, body, language, provider
    message ID, status, and optionally the occurrence and reminder. The owner
    gets a thread UI with reply. Nothing parses keywords; no reply means
    nothing happens.
  - **Phone verification and the household invite:** a `links` action link
    lets a contact claim a household by verifying the phone
    (`authentication/phonecodes`); `verified_at` is written then.
  - **Open:** whether Twilio will approve an ISV Reseller profile for a US sole
    proprietor with no EIN. Confirm with Twilio (and Telnyx, whose ISV
    programme prices lower and which the model ports to unchanged) before
    starting. If the answer is no, an EIN is a free same-day IRS application.
  - **Developing against it:** the Twilio trial is unusable (Twilio's own
    templates, five verified numbers, thirty days). Build on the noop sender
    with a dev-only simulated phone. For texting a few real people, a toll-free
    number verified as `SOLE_PROPRIETOR` is the cheapest route. It needs an
    upgraded account and a public privacy policy and terms.
- **Waitlist** (platform-go `waitlists`): a slot that opens up.
- **Data privacy and retention flows** (`dataprivacy`, `retention`,
  `shredding`). The privacy adapters are written with each store; the flows are
  not.
- **Entitlements gating** (`entitlements`): seats and SMS allowance, and Fijo's
  own pricing.
- **Google Calendar:** a read-only push with a sync button, over
  `authentication/grants` for the OAuth tokens.
- **An explicit guardian table**, only if a case needs it (separated parents
  with different children). Until then every contact on a household is a
  guardian of every attendee on it.

## Cleanup that needs no new product decisions

- **Retire `internal/auth/mint`.** platform-go#874 landed as
  `signin.Service.IssueForPrincipal` (in the pinned v15.1.0). Passkey logins
  should mint through it, and the duplicated mint shape goes away.
- **Recovery codes** (platform-go#867, `signin/recoverycodes`) are in the
  platform and not wired in. Password reset by email is the owner's only way
  back if they lose their passkey.
- **A local mail sink.** localdev's email provider is noop, so a mailed link or
  a reminder is visible only in the debug log. primitives-go has no SMTP
  provider. One would be an additive primitives-go change.
- **Platform version.** Fijo pins platform-go v15.1.0; v15.3.2 is out.
- Not on this list: Fijo's schedule domain is not to be ported onto
  platform-go's `series` package. platform-go#1006 settled that Fijo's grid is
  the proving ground and extraction happens later, from it.
