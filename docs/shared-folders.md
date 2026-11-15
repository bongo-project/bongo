# Sharing folders from a mail client

Bongo shares a mail folder the way it shares a calendar: the owner grants a
person or a group some rights, and the grantee accepts. Folders can be shared
from the web interface or from any IMAP client that speaks the ACL extension
(RFC 4314). Both write the same grants.

Shared folders appear to the grantee under the "other users" prefix
(`Other Users/{owner address}/{folder}` by default, see `imap.other_users_prefix`).

Before anyone shares a folder, upgrade every node of a cluster. A node on an
older binary cannot apply folder grants, so its replicas would diverge.

## Commands

The server advertises `ACL`. The commands are those of RFC 4314:

- `MYRIGHTS` reports what you may do in a folder you can see. The owner holds
  all of `lrswipkxtea`.
- `GETACL` lists the owner and every accepted or pending grant, naming a person
  by their primary address and a group by its address. Grants whose invitation
  has expired are not listed. It needs the `a` right.
- `LISTRIGHTS` says which rights may be granted: the owner's are fixed, and
  anyone else may be given any single letter of `lrswipkxtea`. It needs `a`.
- `SETACL` grants, adds (`+`) or removes (`-`) rights. It needs `a`.
- `DELETEACL` removes one grant. It needs `a`.

A folder you cannot see, including one with a pending invitation or without the
lookup (`l`) right, answers exactly like a folder that does not exist.

## Who can be named

- A local user, by any address that reaches their account.
- A group, only when it is on the owner's domain.

Anything else, whether an external address, an unknown one, a disabled account,
a group of another domain, `anyone`, `authenticated` or a negative identifier
(starting with `-`), is refused with the same text. The answer never says
whether the address exists elsewhere. Sharing is limited per person and hour,
shared with the web share form, so it cannot be used to look up accounts.

The owner's own rights cannot be changed or removed by anyone.

## What a grant does

- A new person gets a pending grant and the same invitation mail the web sends.
  The folder shows in their list once they accept it on the shares page.
- Changing the rights of someone who already holds a grant keeps its state and
  sends no mail.
- A group on the owner's domain is added accepted, with no mail.
- A folder can have at most 50 grants. When it is full, `SETACL` for anyone not
  already on it answers `NO [LIMIT]`, whether or not the address is real.
- Removing every right (`SETACL ... ""`, `-` of everything, or `DELETEACL`)
  revokes the grant.
- Unknown rights (`c` and `d` from RFC 2086 included) are refused with `BAD`.
  Rights are stored in the order `lrswipkxtea`, whatever order the client used.

A person who holds `a` on a folder may grant any right, `a` included, and may
change or remove other people's grants. The grant records who made it.
