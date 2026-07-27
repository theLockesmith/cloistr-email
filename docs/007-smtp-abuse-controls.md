# RFC-007: SMTP Abuse Controls

**Status:** Implemented (detection ladder shipped; FBL ingestion pending enrollment)
**Migration:** `configs/migrations/009_smtp_abuse_controls.sql`

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

### 4. Detection ladder (`internal/abuse/`)

A scanner (default every 15 min) evaluates every account that sent mail in the
window and escalates:

| Rung | Effect | Reversal |
|------|--------|----------|
| `warn` | recorded + logged; no enforcement | expires after 24h |
| `throttle` | limits clamped to 1 msg/min, 10/hr, 50/day, 5 recipients/msg | expires after 6h |
| `hold` | send gate closed, queued mail parked (not bounced) | auto once signals clear and ≥1h has passed |
| `suspend` | platform-wide account disable | **manual only** |

Triggers (`abuse.DefaultThresholds`):

- **Hard bounce rate** over 24h: ≥5% warn, ≥15% throttle, ≥35% hold, ≥60%
  suspend. Rates are ignored below 20 messages, where they are statistical noise.
- **Velocity**: >500 recipients in an hour holds outright, regardless of bounce
  rate. This is the compromised-account tripwire — it fires before any bounce has
  had time to come back.

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

## Not yet built

- **FBL ingestion** — parsing ARF complaint reports and attributing them via
  `sender_pubkey`. Blocked on feedback-loop enrollment with the major providers.
  Complaint rate is a stronger signal than bounce rate and should become a rung
  trigger once available.
- **rspamd** — outbound content scoring. Not deployed.
- **Operator review surface** — marks are currently visible only in logs. An
  admin endpoint listing held accounts with their reasons would make the
  clamped-suspend design usable in practice.
