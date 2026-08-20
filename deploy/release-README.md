# Bongo — test build

Thanks for taking a look. This is a pre-release build shared for feedback; it is not a finished
product and is not supported.

Bongo is a mail and calendar server that runs as a single binary: SMTP, IMAP, POP3, CalDAV and
CardDAV, with a web interface, its own storage (no separate database to install), and full-text
search. `bongo-go` is the server; `bongo-import` brings in existing mail from mbox, maildir, .eml
files, another IMAP server, or Thunderbird.

## Try it in one command

```bash
./bongo-go serve --dev
```

Dev mode needs no configuration. It uses a temporary data directory (deleted on exit), generates
a self-signed certificate, creates `admin@localhost` / `admin`, and starts everything on
non-privileged ports. Open <https://localhost:8080> and accept the certificate warning.

This is the fastest way to form an opinion. Nothing is written outside the temporary directory.

## Running it properly

See `INSTALL.md` in this zip for a real installation: where files go, how to configure it, and how
to point DNS at it. `bongo.example.hcl` is a fully annotated configuration — every option is
documented in place, so it doubles as the configuration reference.

If you are going to receive real mail, read the DNS section carefully. A mail server that is not
set up correctly will have its mail rejected by most receivers, and that is a property of the
internet rather than of this software.

## What is not finished

Being direct about this so you do not waste time reporting things already known:

- **The `agent` server role only implements SMTP.** Setting its role to `imap`, `pop3` or `caldav`
  logs "not yet implemented" and does nothing.
- **Multi-node clustering is not ready to use.** A single server is the supported shape. The
  clustering code exists but the transport is not hardened, and the server deliberately refuses to
  join a cluster unless TLS and gossip encryption are configured.
- **No upgrade path is promised between test builds.** Treat the data directory as disposable.

## Antivirus, if you turn it on

Antivirus scanning works, but it is off by default and needs a running ClamAV daemon. Set
`antivirus.clamav.enabled = true`, point `socket` at your clamd, and add an `antivirus` stage to
the `pipeline` block. With `enabled = false` no scanner is built and nothing pretends to scan.

The part worth understanding before you enable it is what happens when clamd is *not* reachable,
because the answer is deliberate and it is not "carry on":

- By default (`on_failure = "quarantine"`) the message is held in the admin quarantine rather than
  delivered unscanned. Drain the backlog with the quarantine page's bulk **rescan** action once
  clamd is back — rescanned messages are genuinely scanned on the second pass.
- **The recovery window is finite.** Quarantine is auto-purged after `pipeline.quarantine_retention`
  (30 days by default) and the purge deletes the message itself. An outage nobody notices for
  longer than that destroys real mail. Every failure logs at ERROR — alert on it.
- `on_failure = "accept"` delivers unscanned instead, logging each occurrence at ERROR. Choose it
  only if availability matters more than the guarantee that everything was scanned.

Note that clamd being unreachable does **not** stop the server starting; it is handled per message
at delivery time, as above. The one startup-time change: an `agent`-role node listing `antivirus`
in `server.agents` now refuses to start unless `antivirus.clamav.enabled = true`. It previously
started and registered a scanner that passed everything, which is worse than failing loudly — a
node whose entire job is scanning should not run without a scanner configured.

## What feedback is most useful

In rough order:

1. **Anything where mail is lost, duplicated, or silently not delivered.** This is the one class of
   bug that matters more than the rest combined.
2. **Anything that made you stop and read code** to work out what was meant — that is a
   documentation bug, and it is worth reporting even if you solved it.
3. Interoperability: which mail client you used, and what it did that was wrong.
4. Anything that surprised you about the web interface.
5. ClamAV antivirus scanner behaviour: whether clamd scans as expected, whether quarantine
   recovery via the admin "rescan" action works, and what log output you see during failures.

Please include the version below, what you did, what you expected, and what happened. Server logs
are useful; **redact anything sensitive first** — logs can contain email addresses and subject
lines.

## Reporting a security issue

Please report privately rather than in the open, and give a reasonable window to fix it before
disclosing. See the contact address below.

## Version

Run `./bongo-go version` — it reports the version, the exact commit it was built from, and the
build date. Please include that line in any report.

## Contact

<!-- REPLACE BEFORE SHARING: put a real address here, and the same one in SECURITY reports above. -->
FEEDBACK_CONTACT_GOES_HERE

## Terms

<!-- REPLACE BEFORE SHARING: state the terms you are offering this under. -->
This build is shared for evaluation and feedback only. All rights reserved unless stated
otherwise in an accompanying licence file.
