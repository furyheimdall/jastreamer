# syntax=docker/dockerfile:1.7
FROM --platform=$BUILDPLATFORM golang:1.25-alpine@sha256:1ae0735f00daffa3aaf1363a5184c0d2dc55c78e3db4ec70241cdac97bf84b59 AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION
ARG REVISION
ARG CREATED
WORKDIR /src
COPY apps/server/go.mod apps/server/go.sum* ./apps/server/
COPY apps/server ./apps/server
WORKDIR /src/apps/server
RUN test -n "$VERSION" && test -n "$REVISION" && test -n "$CREATED" && CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w -X main.productVersion=$VERSION -X main.sourceRevision=$REVISION" -o /out/jstreamer-server ./cmd/jstreamer-server

FROM alpine:3.22@sha256:14358309a308569c32bdc37e2e0e9694be33a9d99e68afb0f5ff33cc1f695dce
ARG VERSION
ARG REVISION
ARG CREATED
RUN test -n "$VERSION" && test -n "$REVISION" && test -n "$CREATED"
LABEL org.opencontainers.image.title="Jake Streamer Server" \
      org.opencontainers.image.description="Local-first Jake Streamer server" \
      org.opencontainers.image.vendor="furyheimdall" \
      org.opencontainers.image.base.name="alpine:3.22@sha256:14358309a308569c32bdc37e2e0e9694be33a9d99e68afb0f5ff33cc1f695dce" \
      org.opencontainers.image.version="$VERSION" \
      org.opencontainers.image.revision="$REVISION" \
      org.opencontainers.image.created="$CREATED" \
      org.opencontainers.image.source="https://github.com/furyheimdall/jake-streamer" \
      org.opencontainers.image.url="https://github.com/furyheimdall/jake-streamer" \
      org.opencontainers.image.licenses="Apache-2.0"
RUN addgroup -S -g 10001 jstreamer && adduser -S -D -H -u 10001 -G jstreamer jstreamer && apk add --no-cache ca-certificates && mkdir -p /var/lib/jstreamer /etc/jstreamer /app/migrations && chown -R 10001:10001 /var/lib/jstreamer /etc/jstreamer
COPY --from=build /out/jstreamer-server /usr/local/lib/jstreamer-server
COPY apps/server/migrations /app/migrations
COPY packaging/container/health-entrypoint.sh /usr/local/bin/jstreamer-server
COPY LICENSE /usr/share/licenses/jstreamer/LICENSE
COPY packaging/container/THIRD_PARTY_NOTICES /usr/share/licenses/jstreamer/THIRD_PARTY_NOTICES
RUN chmod 0755 /usr/local/bin/jstreamer-server /usr/local/lib/jstreamer-server
ENV JSTREAMER_DATA_DIR=/var/lib/jstreamer \
    JSTREAMER_CATALOG_MIGRATION=/app/migrations/001_catalog.sql \
    JSTREAMER_PLAYBACK_MIGRATION=/app/migrations/002_playback.sql \
    JSTREAMER_PLAYBACK_EXPANSION=/app/migrations/003_todo12.sql
VOLUME ["/etc/jstreamer", "/var/lib/jstreamer"]
USER 10001:10001
WORKDIR /app
EXPOSE 8443
HEALTHCHECK --interval=30s --timeout=5s --retries=3 CMD ["/usr/local/bin/jstreamer-server", "health"]
ENTRYPOINT ["/usr/local/bin/jstreamer-server"]
