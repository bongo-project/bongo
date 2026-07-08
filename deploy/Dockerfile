# syntax=docker/dockerfile:1

ARG VERSION=dev
ARG COMMIT=unknown
ARG DATE=unknown

# ---- Build stage ------------------------------------------------------------
# Pinned to the go directive in go.mod (go 1.25.x) and the templ version used
# by the project. Pure-Go build (modernc sqlite, no cgo) -> fully static binary.
FROM golang:1.25-bookworm AS build
ARG VERSION
ARG COMMIT
ARG DATE

# Install the exact templ version the repo uses so generated code matches.
RUN go install github.com/a-h/templ/cmd/templ@v0.3.1001

WORKDIR /src

# Cache module downloads.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# Copy the rest of the source.
COPY . .

# Generate templ code, then build both binaries as static executables.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    templ generate && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X github.com/bongo-go/bongo-go/internal/version.Version=${VERSION} -X github.com/bongo-go/bongo-go/internal/version.Commit=${COMMIT} -X github.com/bongo-go/bongo-go/internal/version.Date=${DATE}" -o /out/bongo-go     ./cmd/bongo-go && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X github.com/bongo-go/bongo-go/internal/version.Version=${VERSION} -X github.com/bongo-go/bongo-go/internal/version.Commit=${COMMIT} -X github.com/bongo-go/bongo-go/internal/version.Date=${DATE}" -o /out/bongo-import ./cmd/bongo-import

# ---- Runtime stage ----------------------------------------------------------
# Alpine keeps a shell for debugging and ships ca-certificates + tzdata, both
# needed for outbound SMTP over TLS (MX delivery) and correct timestamps.
# su-exec is a minimal setuid/setgid/execvp helper for privilege drop.
# libcap provides setcap, used below to grant cap_net_bind_service as a file
# capability so the non-root process can still bind privileged mail ports.
FROM alpine:3.20
ARG VERSION
ARG COMMIT
ARG DATE

LABEL org.opencontainers.image.title="Bongo"
LABEL org.opencontainers.image.version=$VERSION
LABEL org.opencontainers.image.revision=$COMMIT
LABEL org.opencontainers.image.created=$DATE

RUN apk add --no-cache ca-certificates tzdata su-exec libcap && \
    # Dedicated service account with a fixed uid/gid (10001).  The fixed id
    # lets the host operator chown bind-mounted directories once and forget it.
    # -S = system account, -H = no home dir, -D = no password.
    addgroup -g 10001 -S bongo && \
    adduser  -u 10001 -S -G bongo -H -D bongo && \
    mkdir -p /data /etc/bongo /certs /logs

COPY --from=build /out/bongo-go     /usr/local/bin/bongo-go
COPY --from=build /out/bongo-import /usr/local/bin/bongo-import

# Grant cap_net_bind_service as a file capability on the binary so uid 10001
# can bind privileged ports (25, 587, 143, 993) after the privilege drop in the
# entrypoint.  The capability is only active when the bounding set contains it
# (ensured by cap_add: NET_BIND_SERVICE in docker-compose.yml).
#
# NOTE: this requires the absence of no-new-privileges in docker-compose.yml.
# The Linux kernel ignores file capabilities when PR_SET_NO_NEW_PRIVS is set,
# which would silently prevent binding privileged ports.  See compose comment.
RUN setcap 'cap_net_bind_service=+ep' /usr/local/bin/bongo-go

# Entrypoint script: chowns writable directories, then drops to uid 10001.
COPY deploy/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod 755 /usr/local/bin/entrypoint.sh

# Mail + web + metrics ports (host mapping is defined in compose).
EXPOSE 25 587 143 993 110 995 8080 9090

VOLUME ["/data", "/logs"]

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
CMD ["serve", "--config", "/etc/bongo/bongo.hcl"]
