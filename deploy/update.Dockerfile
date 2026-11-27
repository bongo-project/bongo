# syntax=docker/dockerfile:1

# Image of cmd/bongo-update, the service behind the version check. Build from
# the repository root:
#   docker build -f deploy/update.Dockerfile -t bongo-update .

ARG VERSION=dev
ARG COMMIT=unknown
ARG DATE=unknown

# ---- Build stage ------------------------------------------------------------
# The same pinned base as deploy/Dockerfile (refresh both together with
# `docker buildx imagetools inspect <image:tag>`). No templ step: the binary
# imports only the standard library, internal/updatecheck, internal/version
# and pgx.
FROM golang:1.27.4-bookworm@sha256:4fa7c968ba93f1635670522dbb01a2c78150443e1385746924566079f36570ea AS build
ARG VERSION
ARG COMMIT
ARG DATE

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X github.com/bongo-project/bongo/internal/version.Version=${VERSION} -X github.com/bongo-project/bongo/internal/version.Commit=${COMMIT} -X github.com/bongo-project/bongo/internal/version.Date=${DATE}" -o /out/bongo-update ./cmd/bongo-update

# ---- Runtime stage ----------------------------------------------------------
# ca-certificates for a database connection with sslmode=verify-full. Runs as
# uid 10001 from the first instruction and writes nothing to disk.
FROM alpine:3.24@sha256:be2656061b536c1883c4034790e21d0a09c5aaaf51968b89368a3c0371ea2c9a
ARG VERSION
ARG COMMIT
ARG DATE

LABEL org.opencontainers.image.title="Bongo update service"
LABEL org.opencontainers.image.version=$VERSION
LABEL org.opencontainers.image.revision=$COMMIT
LABEL org.opencontainers.image.created=$DATE

RUN apk add --no-cache ca-certificates && \
    addgroup -g 10001 -S bongo && \
    adduser  -u 10001 -S -G bongo -H -D bongo

COPY --from=build /out/bongo-update /usr/local/bin/bongo-update

EXPOSE 8080

USER 10001:10001

ENTRYPOINT ["/usr/local/bin/bongo-update"]
