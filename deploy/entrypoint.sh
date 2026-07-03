#!/bin/sh
# Container entrypoint — runs as root, fixes ownership, then drops privileges.
set -e

# Fix ownership of the writable bind mounts so uid 10001 can use them.
# /data and /logs are bongo's own state. /certs is written by the root
# certs-dumper; chowning it lets the non-root process read the mail key.
# Best-effort: a single unchownable file (e.g. another service's cert dumped
# into /certs) must not abort startup.
#
# /secrets/dkim.key IS mounted :ro (host bind-mount) and cannot be chowned here.
# The host operator makes it readable once (chown 10001, or chmod 644) — see
# README section 11.
chown -R bongo:bongo /data /logs 2>/dev/null || true
chown -R bongo:bongo /certs 2>/dev/null || true

# Drop to the dedicated non-root user (uid/gid 10001) and exec the main
# process, replacing this shell.  su-exec does setgid+setuid+execvp with no
# shell wrapper, so the binary becomes PID 1 (or the direct child of the
# container init).
#
# The bongo-go binary carries cap_net_bind_service as a file capability
# (set via setcap in the Dockerfile).  The compose file keeps cap_add:
# NET_BIND_SERVICE so the capability is in the bounding set when the kernel
# grants it on execve, allowing uid 10001 to bind ports 25, 587, 143, 993.
exec su-exec bongo:bongo /usr/local/bin/bongo-go "$@"
