# Attachment policy

A domain can block file types in the mail it receives. Matching attachments are
either removed from the message and replaced by a short notice, or the whole
message is held in quarantine.

The policy is set per domain by a global administrator or by an administrator
of that domain, at **Admin > Domains > (domain) > Attachment policy**. A domain
that has no policy is not filtered.

## Settings

| Setting | Meaning |
|---------|---------|
| Enable | The policy applies only while this is ticked. |
| Blocked file extensions | One per line or separated by commas, without the dot. Suggested: `exe`, `scr`, `js`, `vbs`, `bat`, `cmd`, `com`, `jar`, `ps1`, `msi`. |
| Blocked MIME types | One per line. Suggested: `application/x-msdownload`, `application/x-dosexec`, `application/x-msdos-program`, `application/x-ms-installer`, `application/java-archive`. |
| Action | Remove the attachment and leave a notice, or hold the whole message in quarantine. |
| Language of the notice | English when left empty. |

The suggested lists are shown on a domain with no policy and are not applied
until you save. Up to 100 extensions and 100 MIME types are accepted.

An alias domain has no policy of its own: the policy of its target domain
applies.

## Which domain's policy applies

The recipient is resolved to a local user and the policy of that user's primary
address domain is used. An alias address, a catch-all, an alias domain and a
distribution group copy therefore all meet the policy of the domain that
manages the person who receives the mail. An address that does not resolve uses
its own domain, mapped through the alias domain's target; a domain this server
does not host has no policy.

The policy applies to everything that goes through the delivery pipeline:
mail from other servers, mail that local users send to each other, and mail
collected from remote accounts. It is read for every message, so a change
applies to the next message on every node.

## How a file is recognised

* The declared type (`Content-Type`) is compared without parameters and
  without regard to case.
* The file name comes from `filename` in `Content-Disposition` and from `name`
  in `Content-Type`. Both are read, including RFC 2231 names
  (`filename*=UTF-8''...`, continued `filename*0`, `filename*1`) and RFC 2047
  encoded words. Path parts are dropped, control, zero-width and
  bidirectional characters are ignored, trailing dots and spaces are ignored,
  and the case does not matter.
* Only the last extension counts. `invoice.pdf.exe` is an exe; `invoice.exe.pdf`
  is a pdf.
* Every part is inspected, whether or not it is marked as an attachment,
  including inline parts and a message that is a single attachment. Messages
  with LF line ends are read as well as CRLF.
* A `message/rfc822` part (a forwarded message) that is stored unencoded
  (`7bit`, `8bit`, `binary` or no `Content-Transfer-Encoding`, comments
  ignored) is searched too. One with a transfer encoding Bongo does not know is
  held in quarantine, because readers can disagree about what it contains.

**Archives are not opened.** A blocked file inside a zip, 7z, rar or similar
archive is not detected. Neither are encoded `message/rfc822` parts and
encrypted or signed-and-enveloped parts (S/MIME `pkcs7-mime`, PGP). Block the
archive types themselves if that matters to you.

Other limits, listed so you can judge them:

* `message/global` parts (internationalised forwarded messages) are not
  searched: a blocked file inside one is not detected.
* A uuencoded body is not inspected: a blocked file inside one
  (`begin 644 name.exe`) is not detected.
* File names in RFC 2231 or RFC 2047 charsets that do not keep ASCII as ASCII
  (for example UTF-7 or IBM037) are read as plain bytes, so a blocked extension
  written that way is not detected.

**Ambiguous messages are held.** A message is quarantined, not rewritten, when
parsers could read it differently: more than one `Content-Type`,
`Content-Disposition` or `Content-Transfer-Encoding` header on one part, a
repeated or missing `boundary` parameter, a multipart type that cannot be
parsed, a structured message the main parser cannot read, a forwarded message
with an unknown transfer encoding, more than 256 parameters in one
`Content-Type` or `Content-Disposition` header, or header blocks over 1 MiB per
part or 4 MiB in total.

## What the removal does

The blocked part is replaced by a `text/plain` part that names the file, the
domain and the rule that matched, in the language set on the policy (English by
default). In a multipart message the message headers are not changed. When the
whole message is the blocked file (a message that is a single attachment, at the
top level or forwarded inside another), its `Content-Type`,
`Content-Transfer-Encoding`, `Content-Disposition` and `Content-Description`
headers are replaced by those of the notice, and `MIME-Version` is added if it
was missing; every other header is kept.

The changed message is stored as a new message; the original is kept until it is
no longer needed and is then cleaned up. If the message cannot be rewritten and
checked again with confidence, because the structure is too deep or too large or
the result still holds a blocked part, it is held in quarantine instead. A
policy whose action is to remove can therefore still quarantine a message.

Envelopes are normally one per recipient, so each recipient meets their own
domain's policy. An envelope that still lists several recipients is checked
against the merged policies of all their domains: if any of them quarantines,
the message is quarantined for everyone, and if all of them remove, the
stripped copy replaces the message for every recipient on the envelope,
including recipients on domains that have no policy.

If the policy, or the lookup of which domain's policy applies, cannot be read
from the store, the message is not delivered: it is moved to the pipeline's
error spool and the error is logged, as for other pipeline failures. It is not
retried automatically.

A message signed with S/MIME or PGP (`multipart/signed`) loses its signature
when a part is removed.

### DKIM and ARC

DKIM is checked on the message as it arrived, before anything is removed. The
result is recorded and appears in the `Authentication-Results` header and in the
message details, so the stored copy still shows `dkim=pass` when the sender's
signature was good. The `DKIM-Signature` and `ARC-*` headers are kept exactly as
they arrived, but because the body changed they no longer verify if someone
checks the stored copy again. Bongo does not verify ARC chains.

One-click unsubscribe is affected by this. Bongo sends a one-click request only
when the stored copy's DKIM signature verifies, so for a stripped message the
Unsubscribe button falls back to the panel with a link you open yourself (see
[unsubscribe.md](unsubscribe.md)).

A forward made by a user's mail filter carries the stripped message, so
forwarding cannot be used to pass a blocked file on. The DKIM signature of such a
forward does not verify at its destination.

## Quarantine

With the quarantine action, or when a message cannot be stripped safely, the
message is held in the quarantine list with the stage `attachment_policy` and the
matched rule. Releasing it delivers the original message, with the attachment: an
administrator who releases a message overrides the policy. Rescanning checks it
again against the current policy.

## Configuration

The stage is `attachment_policy` and takes no backend. It is part of the default
stage list, after `antivirus` and before `rules`. A configuration that lists the
`stage` blocks itself does not pick it up, so add it by hand:

```hcl
pipeline {
  stage "antispam" {
    enabled = true
    backend = "builtin"
  }
  stage "antivirus" {
    enabled = false
  }
  stage "attachment_policy" {
    enabled = true
  }
  stage "rules" {
    enabled = true
  }
}
```

Changing the stage list needs a restart. When upgrading a cluster, upgrade every
node before adding the stage to an explicit list: an older binary refuses a
configuration that names it, and an older node that is still running does not
enforce policies.

## Logs

Log lines use the `attachment-policy` component. They carry the message id, the
domain, the rule that matched and the number of parts, never a file name or any
message content.
