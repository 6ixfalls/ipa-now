# syntax=docker/dockerfile:1

FROM node:22.15.0-alpine3.21 AS frontend
WORKDIR /src/web
RUN npm install --global pnpm@11.24.0
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm run build

# SQLite requires CGO. Build and run against the same musl distribution.
FROM golang:1.26.5-alpine3.23 AS backend
WORKDIR /src
ENV CGO_ENABLED=1
RUN apk add --no-cache build-base
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY --from=frontend /src/internal/web/dist/ ./internal/web/dist/
RUN go build -mod=readonly -trimpath -buildvcs=false -o /out/ipa-now ./cmd/ipa-now

FROM alpine:3.23 AS server
RUN apk add --no-cache ca-certificates libstdc++ \
    && addgroup -g 10001 ipa-now \
    && adduser -D -u 10001 -G ipa-now -H -h /var/lib/ipa-now -s /sbin/nologin ipa-now \
    && mkdir -p /var/lib/ipa-now \
    && chown 10001:10001 /var/lib/ipa-now \
    && chmod 0700 /var/lib/ipa-now
COPY --from=backend /out/ipa-now /usr/local/bin/ipa-now
ENV IPA_NOW_DATA_DIR=/var/lib/ipa-now \
    IPA_NOW_LISTEN=127.0.0.1:8080 \
    XDG_CACHE_HOME=/var/lib/ipa-now/cache
WORKDIR /var/lib/ipa-now
USER 10001:10001
EXPOSE 8080
STOPSIGNAL SIGTERM
ENTRYPOINT ["/usr/local/bin/ipa-now"]
