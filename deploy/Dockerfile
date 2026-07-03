# syntax=docker/dockerfile:1

# ---- Build stage ------------------------------------------------------------
# Pinned to the go directive in go.mod (go 1.25.x) and the templ version used
# by the project. Pure-Go build (modernc sqlite, no cgo) -> fully static binary.
FROM golang:1.25-bookworm AS build

# Install the exact templ version the repo uses so generated code matches.
RUN go install github.com/a-h/templ/cmd/templ@v0.3.977

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
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/bongo-go     ./cmd/bongo-go && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/bongo-import ./cmd/bongo-import

# ---- Runtime stage ----------------------------------------------------------
# Alpine keeps a shell for debugging and ships ca-certificates + tzdata, both
# needed for outbound SMTP over TLS (MX delivery) and correct timestamps.
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata && \
    mkdir -p /data /etc/bongo /certs /logs

COPY --from=build /out/bongo-go     /usr/local/bin/bongo-go
COPY --from=build /out/bongo-import /usr/local/bin/bongo-import

# The process runs as root to bind privileged mail ports, but the compose file
# drops ALL Linux capabilities except NET_BIND_SERVICE and sets
# no-new-privileges, so a compromise has almost no privileged surface. Moving to
# a dedicated non-root UID is a further step (needs bind-mount UID alignment for
# /logs and the DKIM key).

# Mail + web + metrics ports (host mapping is defined in compose).
EXPOSE 25 587 143 993 110 995 8080 9090

VOLUME ["/data", "/logs"]

ENTRYPOINT ["bongo-go"]
CMD ["serve", "--config", "/etc/bongo/bongo.hcl"]
