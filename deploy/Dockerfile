# syntax=docker/dockerfile:1

ARG VERSION=dev
ARG COMMIT=unknown
ARG DATE=unknown

# ---- Build stage ------------------------------------------------------------
# Pinned to the go directive in go.mod (go 1.26.x) and the templ version used
# by the project. Pure-Go build (modernc sqlite, no cgo) -> fully static binary.
FROM golang:1.26-bookworm AS build
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
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X github.com/bongo-project/bongo/internal/version.Version=${VERSION} -X github.com/bongo-project/bongo/internal/version.Commit=${COMMIT} -X github.com/bongo-project/bongo/internal/version.Date=${DATE}" -o /out/bongo-go     ./cmd/bongo-go && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X github.com/bongo-project/bongo/internal/version.Version=${VERSION} -X github.com/bongo-project/bongo/internal/version.Commit=${COMMIT} -X github.com/bongo-project/bongo/internal/version.Date=${DATE}" -o /out/bongo-import ./cmd/bongo-import

# ---- Runtime stage ----------------------------------------------------------
# Alpine keeps a shell for debugging and ships ca-certificates + tzdata, both
# needed for outbound SMTP over TLS (MX delivery) and correct timestamps.
#
# The image runs as uid 10001 from the first instruction: there is no root
# entrypoint, no su-exec and no file capabilities. Privileged mail ports (25,
# 587, 143, 993) bind because the container sets
# net.ipv4.ip_unprivileged_port_start=0 (see docker-compose.yml), which is
# compatible with no-new-privileges and an empty capability set.
FROM alpine:3.20
ARG VERSION
ARG COMMIT
ARG DATE

LABEL org.opencontainers.image.title="Bongo"
LABEL org.opencontainers.image.version=$VERSION
LABEL org.opencontainers.image.revision=$COMMIT
LABEL org.opencontainers.image.created=$DATE

RUN apk add --no-cache ca-certificates tzdata && \
    # Dedicated service account with a fixed uid/gid (10001).  The fixed id
    # lets the host operator chown bind-mounted directories once and forget it.
    # -S = system account, -H = no home dir, -D = no password.
    addgroup -g 10001 -S bongo && \
    adduser  -u 10001 -S -G bongo -H -D bongo && \
    mkdir -p /data /etc/bongo /certs /logs && \
    # Fresh named or anonymous volumes inherit this ownership. Bind mounts do
    # not: the host operator chowns them (deploy/README.md section 11).
    chown 10001:10001 /data /logs /certs

COPY --from=build /out/bongo-go     /usr/local/bin/bongo-go
COPY --from=build /out/bongo-import /usr/local/bin/bongo-import

# Mail + web + metrics ports (host mapping is defined in compose).
EXPOSE 25 587 143 993 110 995 8080 9090

VOLUME ["/data", "/logs"]

USER 10001:10001

ENTRYPOINT ["/usr/local/bin/bongo-go"]
CMD ["serve", "--config", "/etc/bongo/bongo.hcl"]
