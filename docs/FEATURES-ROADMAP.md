# Features Roadmap — cloistr-email

Forward-looking feature ideas that are **not** tied to a specific RFC's
implementation phases. RFC-scoped work (e.g. Blossom storage) lives in its own
`docs/00N-*.md`; this file is for product features we want to capture before
they're scheduled.

> Status legend: 💡 idea · 📐 designing · 🚧 in progress · ✅ shipped

---

## Distribution groups (group addresses)

**Status:** 💡 idea

A single address that fans out to multiple recipients. Known by many names
depending on the ecosystem:

- **Microsoft / Exchange:** distribution group / distribution list (DL)
- **Google Workspace:** Google Group
- **Generic SMTP / Postfix / Sendmail:** mailing list, alias, `:include:` file
- **RFC terminology:** group mailbox / address list

### What we want

- An address like `team@cloistr.xyz` that expands to a set of member
  addresses (internal `@cloistr.xyz` npub-backed users and/or external
  SMTP addresses).
- Sending to the group delivers to every current member.
- Membership is managed (who can post, who can join, owner/moderator roles).

### Open questions to resolve at design time

1. **Expansion point** — expand on inbound receipt (transport layer) vs. at
   the API/compose layer? Inbound expansion is required for external senders
   emailing the group.
2. **Encryption semantics** — NIP-44 is pairwise (per-recipient pubkey). A
   group send must encrypt once per member, or the group needs its own
   keypair (cf. NIP-29 groups / shared group key). This interacts directly
   with RFC-002/003. **Biggest unknown.**
3. **Membership source of truth** — Postgres table vs. a Nostr event
   (e.g. NIP-51 lists / NIP-29 group membership) so it's user-sovereign and
   portable, consistent with the rest of Cloistr.
4. **Loop / amplification protection** — group-to-group membership, mail loops,
   and the spam-amplification surface a fan-out address creates.
5. **Reply semantics** — reply-to-group vs. reply-to-sender; `Sender:` /
   `List-*` headers (RFC 2369 / RFC 2919) for proper mailing-list behavior.
6. **External member delivery** — DKIM/SPF alignment when re-sending to
   external members (we become the resending MTA → ARC / rewriting `From`).

### Why it's worth capturing now

The encryption-per-member question (open question 2) overlaps with the
Blossom/NIP-44 content model in RFC-002/003. Decisions we make there (e.g.
whether content is encrypted pairwise or under a group key) should be made
with group addresses in mind so we don't have to re-architect later.

---

## (Add further feature ideas below)
