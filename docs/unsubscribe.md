# Unsubscribing from mailing lists

Open a message from a mailing list or newsletter. When the sender offers a way
to unsubscribe, an Unsubscribe control appears above the message text. Bongo
reads it from the message's `List-Unsubscribe` header (RFC 2369) and, for
one-click, the `List-Unsubscribe-Post` header (RFC 8058). Nothing is stored:
the headers are read from the message each time it is shown.

## What you can see

| The message offers | What you get |
|--------------------|--------------|
| An https address, `List-Unsubscribe-Post: List-Unsubscribe=One-Click`, and a valid DKIM signature | An Unsubscribe button. Bongo sends the request to the sender for you and tells you whether it went through. |
| An https address without that header, or whose signature cannot be checked | A disclosure that names the website. Continue opens it in your own browser; Bongo never contacts it. |
| A `mailto:` address | A link that opens the composer with the address, subject and body filled in. You send the message yourself, so the usual sending limits apply and a copy goes to Sent. |
| Only an `http:` address, or any other kind of address | Nothing. |

The signature is checked when you click, not when the message is drawn. If it
cannot be verified, Bongo does not contact the sender and shows the website
link instead. A signature can fail to verify on the stored copy because
Bongo replaces some trace headers (`Authentication-Results`, `X-Spam-*`) on
delivery; a sender whose signature covers those headers always falls back to
the link.

Only the first https address and the first `mailto:` address are used. The
website address must use port 443 (or 8443) and carry no user name or
password.

## Spam

There is no Unsubscribe control for a message in Junk or below it, or for a
message the spam filter tagged as spam. Unsubscribing from spam confirms to the
sender that your address is read. The message says to report it as spam
instead.

## Mailing lists view

Each list in Views > Mailing lists has an Unsubscribe link. It uses the newest
message of that list in the folders the list view shows (not Junk, Trash, Sent
or Drafts) and offers the same choices as above.

## Limits

One-click requests are limited to 20 per hour per user, on each node of a
cluster. Beyond that Bongo answers "too many requests" and asks you to try
later.

## What Bongo sends

The request is an HTTPS POST with the body `List-Unsubscribe=One-Click`. It
carries no cookies, names no user or domain (`User-Agent: Bongo-Unsubscribe/1`),
follows no redirect, gives up after ten seconds and refuses any address on a
private or internal network. Whatever the website answers is not shown; you see
only a fixed success or failure message. Bongo's logs record your user id, the
message id and the website's host name, never the full address, which usually
contains a token that identifies you.
