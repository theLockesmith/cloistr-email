# RFC-007: SMTP Abuse Controls

**Status:** Implemented (FBL ingestion built; awaiting provider enrollment to produce data)
**Migrations:** `configs/migrations/009_smtp_abuse_controls.sql`, `010_fbl_complaints.sql`

## Problem

Anyone can generate a Nostr keypair. If a keypair alone buys outbound SMTP, the
service is a free open relay with infinite identities — and the first thing that
happens is the sending IPs and every served domain get blocklisted, which breaks
mail for the legitimate users too.

Abuse control here therefore protects existing users' deliverability at least as
much as it protects third parties.

## Design constraints

**Everything is keyed on the sender's Nostr pubkey, never on IP.** A user's
messages must not be correlatable by network address. This rules out the usual
IP-reputation approaches and is why identity tier does the work instead.

**Enforcement escalates.** Every automated action has a false-positive rate, so
the cheap reversible responses come first and the expensive irreversible one is
last — and, by default, is not automatic at all.

**Held mail is retained, not bounced.** A wrongly-held sender loses time, not
messages.

## Layers

### 1. Tier send-rights (`internal/email/sendgate.go`)

| Tier | Outbound |
|------|----------|
| anonymous (extension-only / auto-assigned address) | **receive-only** |
| named (claimed address) | allowed, standard limits |
| named + new (< 7 days) | allowed, 20 recipients/day |
| paid / WoT-vouched / operator-elevated | allowed, per-account windows lifted |

Anonymous being receive-only is the sybil control: infinite free keys must not
mean infinite free outbound. In `standalone` mode (`CLOISTR_MODE`) everyone
resolves as `named`, so self-hosters keep full send rights.

### 2. Rate limits (`internal/ratelimit/`)

Fixed-window counters in Dragonfly, so they hold across all backend replicas
rather than per-pod. Two dimensions — message count and recipient count — at both
per-account and domain-wide scope. Recipients are the metric that actually
matters for spam.

Defaults: 5 msg/min, 100/hr, 500/day, 50 recipients/msg, 1000 recipients/day,
25 MB max per account; 300 msg/min and 5000 recipients/hr across the domain.

A denial consumes no quota — a rejected send must not burn the account's budget.

### 3. Bounce attribution (`internal/transport/bounce.go`)

Bounces are the primary behavioural signal: a sender blasting addresses it never
legitimately collected shows up here first. Each bounce is attributed to the
account that sent the original message:

- **Outbound failures** carry the pubkey directly from the queue row's metadata.
- **Inbound DSNs** are resolved through `outbound_queue`: exact by `Message-ID`,
  falling back to the most recent message sent to that recipient within 7 days,
  because remote MTAs routinely rewrite or drop the `Message-ID`.

Unattributed bounces are stored with `sender_pubkey` NULL and simply do not
count against anyone.

### 4. Feedback-loop complaints (`internal/transport/fbl.go`)

When a recipient at a large provider hits "report spam", enrolled senders get an
ARF report (RFC 5965) back. This is stronger evidence than a bounce — a bounce
means the address was wrong, a complaint means a real person did not want the
mail — and providers begin throttling a sender around a **0.1%** complaint rate,
far below anything a bounce rate would flag.

Reports are intercepted on the inbound path *before* the bounce check, since an
ARF report is also a `multipart/report` and would otherwise be misfiled as a
bounce. Attribution reuses the same resolver as bounces.

**This produces no data until the sending domains are enrolled** in each
provider's FBL programme (Microsoft SNDS/JMRP, Yahoo/AOL CFL, and so on). That
is an operator action; the code path is inert but complete until then.

### 5. Detection ladder (`internal/abuse/`)

A scanner (default every 15 min) evaluates every account that sent mail in the
window and escalates:

| Rung | Effect | Reversal |
|------|--------|----------|
| `warn` | recorded + logged; no enforcement | expires after 24h |
| `throttle` | limits clamped to 1 msg/min, 10/hr, 50/day, 5 recipients/msg | expires after 6h |
| `hold` | send gate closed, queued mail parked (not bounced) | auto once signals clear and ≥1h has passed |
| `suspend` | platform-wide account disable | **manual only** |

Triggers (`abuse.DefaultThresholds`):

- **Hard bounce rate** over 24h (per message): ≥5% warn, ≥15% throttle, ≥35%
  hold, ≥60% suspend. Ignored below 20 messages, where rates are statistical
  noise.
- **Complaint rate** over 24h (per *recipient*, since a complaint comes from one
  person): ≥0.1% warn, ≥0.3% throttle, ≥0.5% hold. Ignored below 200 recipients.
  Complaints never warrant a suspend on their own — unwanted is not the same as
  fraudulent, and the platform-wide hammer should not fall on evidence that
  indirect.
- **Velocity**: >500 recipients in an hour holds outright, regardless of bounce
  rate. This is the compromised-account tripwire — it fires before any bounce has
  had time to come back.

Each signal proposes a rung and the account lands on the highest; they never
cancel each other out, because a clean bounce rate says nothing about whether
recipients want the mail.

Soft bounces are recorded but are not actionable on their own: a full mailbox
says nothing about the sender.

Marks live in Dragonfly rather than a `mailboxes` column, because every rung
below `hold` is meant to expire on its own; a throttle that needs a human to lift
it is just a slow suspend.

### Why auto-suspend is off by default

On this platform a suspend sets `users.enabled = FALSE`, which revokes access to
**every** Cloistr service — drive, sheets, relay, the lot — not just email.
Losing your documents because your mail bounced is not a proportionate response,
so the ladder tops out at `hold` and records that a suspend was warranted, leaving
the decision to an operator.

Set `ABUSE_AUTO_SUSPEND=true` only once the thresholds have been calibrated
against real traffic.

## Configuration

| Env var | Default | Meaning |
|---------|---------|---------|
| `ABUSE_DETECTION_ENABLED` | `true` | Run the detection scanner |
| `ABUSE_SCAN_INTERVAL` | `15m` | How often active senders are re-evaluated |
| `ABUSE_AUTO_SUSPEND` | `false` | Permit the ladder's top rung |
| `CLOISTR_MODE` | `standalone` | `platform` enables tier gating |

Rate limits and thresholds are operator-configurable in code
(`ratelimit.DefaultLimits`, `abuse.DefaultThresholds`); a self-hoster can raise
or disable any of them.

## Schema (migration 009)

```
email_bounces.sender_pubkey    CHAR(64)  -- attribution, nullable
mailboxes.send_enabled         BOOLEAN   -- email-local gate (hold/suspend rungs)
mailboxes.send_elevated        BOOLEAN   -- lifted per-account windows
mailboxes.send_suspended_at    TIMESTAMP -- when the ladder last closed the gate
```

`mailboxes.send_enabled` is deliberately distinct from `users.enabled`: the
former is email-local and owned by this service, the latter is the platform-wide
hammer owned by cloistr-me.

## Schema (migration 010)

```
email_complaints  -- ARF spam complaints, attributed via sender_pubkey
```

## Not yet done

- **`GRANT SELECT ON user_quota_usage TO cloistr_email`** — the storage
  reconciler reads back its own component to compute a delta, and the role
  cannot currently see the table. Same grant class as the `get_user_tier`
  blocker below. Until then the reconciler logs the remedy and skips; email
  storage is not counted against the shared pool. Only bites in `platform` mode —
  in `standalone` the reconciler does not start at all, since there is no shared
  pool to be a component of.
- **`ALTER FUNCTION public.get_user_tier(character) SECURITY DEFINER`** —
  spine-side, and required *before* flipping `CLOISTR_MODE=platform`, or every
  send fails its tier lookup.
- **FBL enrollment** — operator action. Register the sending domains with
  Microsoft SNDS/JMRP, Yahoo/AOL CFL and the rest, pointing each at an address
  this service receives. The ingestion path and the complaint-rate rung are
  already live; they simply see nothing until reports start arriving.
- **rspamd** — outbound content scoring. Not deployed.
- **Operator review surface** — marks are currently visible only in logs. An
  admin endpoint listing held accounts with their reasons would make the
  clamped-suspend design usable in practice.
