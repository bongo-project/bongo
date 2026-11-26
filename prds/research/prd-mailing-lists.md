# PRD: Mailing lists

Status: research. Added to the roadmap by the operator on 2026-11-26. Release
not yet assigned. One-click unsubscribe for mail a user receives is a
separate, smaller item: v0.17.0 US-019 (`prd-administration-v0.17.md`).

## 1. Introduction / Overview

v0.16.0 shipped distribution groups: an admin-managed address that delivers
to a fixed set of local users. A mailing list is the other half of backlog
B10 (`prd-hardening-v0.9.md` 8.2), which the v0.16 PRD named as out of scope
and in need of its own research PRD (`prd-sharing-contacts-v0.16.md`
section 5):

- subscribers who are not users of the server;
- people subscribing and unsubscribing themselves, by email, on a web page
  and with one click;
- list headers (`List-Id`, `List-Unsubscribe`, `List-Post` and the rest of
  RFC 2369 and RFC 8058) on everything the list sends;
- moderation and archives;
- sending to outside mailboxes in a way that survives DMARC.

This PRD records what exists, the questions a design has to answer, and
candidate stories. Nothing here is scheduled.

## 2. Current state (checked against `main` at `a0d2636` on 2026-11-26)

- **Distribution groups** (`internal/store/group.go`, `groupcommand.go`,
  `group_expand.go`; `docs/groups.md`). One composite Raft command
  (`/__group__/`) writes the group, its address record and the member index.
  Members are local, non-resource users only, at most 1000
  (`MaxGroupMembers`), with no nesting. Expansion happens at SMTP RCPT and in
  web compose; each member gets an ordinary local envelope sharing one blob.
- **Posting policy** per group: anyone, domain, members or owners, enforced
  at RCPT and in web compose (`GroupStore.PostingAllowed`).
- **Loop and volume guards:** `X-Bongo-Group` marks a copy and a message
  already carrying it is discarded for that group; the per-sender fan-out cap
  `smtp.limits.group_fanout_per_hour` (default 5000) is in memory, per node.
- **Outbound:** the relay queue (`/queue/pending/`, `/queue/bounced/`) sends
  one envelope per message with retries and DSNs. Per-domain DKIM signing
  exists (`internal/pipeline/deliver/headers.go`). SRS rewrites the envelope
  sender of forwarded mail (`smtp.srs_max_age_days`). There is no ARC
  signing and no VERP.
- **Reading lists:** `List-Id` is stored on `MailMeta` (field 22) and drives
  the Mailing lists view; `List-Unsubscribe` is used only to suppress
  vacation replies.

Nothing today accepts an external address as a member, stores a
subscription, handles a list's bounces, or adds list headers.

## 3. Goals

- An admin or domain admin can create a list on a hosted domain, with owners
  and moderators, and delete or change it.
- Anyone can subscribe with confirmation (double opt-in) and unsubscribe at
  any time, by email, on a web page and with RFC 8058 one-click.
- Mail sent by the list is accepted by large receivers: correct list
  headers, DKIM-signed by the list's domain, and a DMARC strategy that does
  not get the list's mail rejected.
- Bounces disable dead addresses automatically.
- A list cannot be turned into a spam cannon or a subscription-bombing tool.
- Subscriber data is handled as personal data: consent recorded, exportable
  and deletable.

## 4. Design questions

1. **Groups or a new kind?** Extend `Group` (a `kind` of `list`, external
   members) or a separate list record. Groups resolve to local envelopes and
   are capped at 1000; a list may need tens of thousands of external
   subscribers, so the member set probably cannot live inside one protobuf.
   Proposal to evaluate: a list record plus one key per subscriber
   (`/system/list/{id}/sub/{hash}`), changed by a composite command.
2. **DMARC.** A list that changes the message (subject tag, footer) breaks
   the author's DKIM signature; with the author's From kept, a domain with
   `p=reject` gets its posts rejected by receivers. Options: rewrite From to
   the list address ("Name via List") for authors whose domain has a strict
   policy (Mailman's approach), never modify the message, or add ARC signing
   and rely on receivers honouring it. Probably From rewriting plus ARC.
3. **Envelope sender and bounces.** VERP (`list+bounce-{token}@`) so each
   bounce names its subscriber; a bounce score with automatic disabling and
   a probe before removal.
4. **Sending volume.** One post to N external subscribers is N relay
   envelopes, or envelopes batched per receiving domain. The queue, the
   outbound recipient limits and the fan-out cap must all account for list
   traffic, and one list must not starve other outbound mail.
5. **Subscribe and unsubscribe surfaces.** `-request` and `-subscribe` /
   `-unsubscribe` addresses, a public web page per list, and a public
   one-click endpoint. The public routes are unauthenticated and
   internet-facing: signed, expiring tokens, rate limits, no existence
   oracle for private lists, and confirmation mail that cannot be abused to
   flood a third party.
6. **Moderation.** A held-message queue for non-members, first posts or
   everything; moderators approve or reject from the web and by email reply.
7. **Archives.** None, private (members), or public; web view and mbox
   download; whether archives are ordinary IMAP folders of a list account.
8. **Digests.** Wanted, or out of scope?
9. **Privacy.** What a subscriber record holds (address, consent time and
   method, source IP?), retention after unsubscribe, export and deletion
   on request, and whether member lists are visible to members.
10. **Mixed versions.** Any new composite command needs the usual
   upgrade-every-node guardrail; an older node must fail closed for a list
   address, as it does for a group.

## 5. Candidate stories (to be sized when the release is assigned)

- Lists: create, edit and delete; owners and moderators; posting policy;
  list settings (subject prefix, reply-to, footer).
- External subscribers with double opt-in; subscriber management for owners
  (add, remove, import, export).
- Self-service: email commands, web subscribe and unsubscribe page,
  RFC 8058 one-click endpoint, RFC 2369 headers on list mail.
- Sending: DKIM for the list domain, From rewriting for strict-DMARC
  authors, ARC signing, VERP envelope sender.
- Bounce processing with scores, probes and automatic disabling.
- Moderation queue on the web and by email.
- Archives.
- Abuse limits: per-list sending caps, confirmation-mail rate limits,
  outbound queue fairness.

## 6. Non-goals (for now)

- Newsletter and campaign features: templates, tracking pixels, click
  tracking, open rates.
- NNTP gateways.
- Importing lists from Mailman, Sympa or other list servers (may come
  later as its own item).

## 7. Research to do before stories are written

- Read how Mailman 3, Sympa and mlmmj handle DMARC, VERP and bounce scoring,
  and what Gmail's and Yahoo's sender requirements (2024 onwards) ask of
  lists: one-click unsubscribe, spam rate, authentication.
- Measure the outbound queue with a 10,000-recipient post using the load
  test bundle (`make loadtest-bundle`, `prds/active/prd-load-test-v0.15.4.md`).
- Decide question 1 (data model) first: every other answer depends on it.

## 8. Open questions for the operator

1. Who may create lists: global admins only, domain admins, or users?
2. Are public archives wanted at all?
3. Is From rewriting acceptable, given that it changes how posts look in
   readers' inboxes?
4. Target scale for one list: hundreds, thousands or tens of thousands of
   subscribers?
5. Are digests wanted?

### 8.1 Decisions

1. (operator, 2026-11-26) A full mailing-list server is a research item with
   no release yet. One-click unsubscribe for received mail goes into v0.17.0
   as US-019 of `prd-administration-v0.17.md`.

### 8.2 Candidates for later
