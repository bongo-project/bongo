# Per-user features and send limits

An administrator can switch features off for one person and give them their
own send limits. Both are on the user's page in the admin area
(`/admin/users/{id}`, card "Features and limits"). A global administrator can
edit anyone; a domain administrator can edit people on their own domains, and
not a global administrator.

## Features

Untick a feature to switch it off.

| Feature | What switching it off does |
|---|---|
| IMAP, POP3, SMTP submission | Sign-in over that protocol is refused exactly like a wrong password. A session that is already open ends on its next command. |
| CalDAV and CardDAV | Every request is refused with the same 401 a wrong password gets. |
| Web interface | Web sign-in is refused like a wrong password, and open web sessions end on their next request. |
| Forwarding | The settings section is hidden. The account forward and any forward a rule asks for are not sent. Mail is still delivered to the person's own mailbox, including when "keep a copy" was off. |
| Filter rules | The rules page is hidden (the route answers 404) and none of the person's rules run. |
| Vacation reply | The page is hidden and no reply is sent. |

Switching one protocol off does not end the person's sessions on the others. If
the person's profile cannot be read when mail arrives, forwarding, rules and
vacation are skipped (fail closed) and the mail is delivered normally.

Web access cannot be switched off for the last administrator who can still sign
in to the web interface. Deleting, demoting or disabling an administrator is
held to the same rule. The decisions are serialised by a lock in each server
process only, so concurrent requests on one node cannot both pass. The check
counts administrators from the node-local users table and reads their
profiles, then writes one profile, so two administrators acting at the same
moment on different nodes can both pass it, and a node whose users table lags
behind (an account created on another node a moment ago) can count wrongly.

A domain administrator cannot edit their own switches or limits, and can set a
limit only at or below the global value; only a global administrator can set a
limit above it. Domain administrators of the same domain can edit each other's
switches and limits: one can switch features off for another, or set another's
limits back to 0 (the global value), including limits a global administrator
set. Only a global administrator is out of their reach.

Resource and shared inbox accounts never sign in and have no card.

## Send limits

Three limits, each 0 for "use the server-wide value":

- messages per minute (0 to 10000), counted for submission and web compose
  together, only for messages with external recipients;
- external recipients per hour (0 to 100000), shared by submission and web
  compose;
- recipients per message (0 to 1000), counted in To, Cc and Bcc together.

A value above 0 replaces the server-wide one, whether stricter or looser, and
applies even when the server-wide limit is switched off. 0 never means
unlimited. Recipients per hour must not be below recipients per message when
both are set.

The server-wide recipients per message is `smtp.limits.max_recipients`
(default 100). A value of 0 or below there means the default of 100, not
unlimited, for submission, web compose and incoming mail alike. A per-user
value can raise it to at most 1000. Because of that, the submission ports
advertise `LIMITS RCPTMAX=1000` (or the server-wide value when it is higher)
and enforce each person's own cap themselves, answering `452 4.5.3` at it, so
a client that trusts RCPTMAX sees the temporary refusal and sends the rest in
a further transaction. Port 25 keeps advertising and enforcing the server-wide
value.

The per-user limits apply only to mail a person sends through submission or
web compose. Forwards (the account forward and rule forwards) are not counted
against them; they have their own server-wide cap,
`pipeline.forward_max_per_hour`, and a forward of mail the person wrote still
draws on the server-wide recipients-per-hour budget when deciding whether it
is signed. Vacation replies are not counted either; they have their own fixed
per-person cap of replies per hour.

Over a limit, SMTP answers `452` (recipients per message: `452 4.5.3`) and web
compose answers 429 (400 for too many recipients). Counters are in memory on
each node, like the server-wide ones, so a restart or another node starts from
zero. Changes apply to an open submission session on its next command.

## Upgrading

The two fields (`UserProfile.disabled_features`, field 17, and `send_limits`,
field 18) are additive. A node on an earlier binary keeps them when it rewrites
a profile but enforces nothing: it lets a switched-off protocol sign in,
forwards, runs rules and vacation replies, and uses the server-wide limits.
Upgrade every node before relying on these settings.
