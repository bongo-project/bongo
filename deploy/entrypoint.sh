#!/bin/sh
# Container entrypoint — runs as root, fixes ownership, then drops privileges.
set -e

# Fix ownership of writable and readable paths that are mounted into the
# container.  Named Docker volumes (bongo-data, bongo-certs) are created with
# root ownership; bind-mounted ./logs may also arrive root-owned from the host.
#
# /certs is NOT mounted :ro so we can repair ownership here after certs-dumper
# writes renewed certificate files as root.  The next restart of bongo will
# pick up the chown before the process reads the certs.
#
# /secrets/dkim.key IS mounted :ro (host bind-mount).  We cannot chown it here
# without affecting the host file.  The host operator must run once:
#   chown 10001 deploy/secrets/dkim.key
# See README §11 for details.
chown -R bongo:bongo /data /logs /certs

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
