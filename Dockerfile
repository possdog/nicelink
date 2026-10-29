# syntax=docker/dockerfile:1

# Multi-stage build for nicelink.
#
# Runtime notes:
# - The container runs as root on purpose: Fly.io mounts volumes as
#   root:root, and the app must write link_shortener.bolt and secret.key
#   into the mounted /data directory.
# - PORT is set by the Fly platform (and defaulted here for local docker
#   runs); the app reads it to pick its listen address.
# - DATA_DIR points at the persistent volume mounted via [mounts] in
#   fly.toml, so the bbolt database and the server secret survive machine
#   restarts and redeploys.

FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/nicelink ./cmd/nicelink

FROM alpine:3.22
RUN mkdir -p /data
COPY --from=build /out/nicelink /usr/local/bin/nicelink
ENV PORT=8080 \
    DATA_DIR=/data
EXPOSE 8080
CMD ["nicelink"]
