# syntax=docker/dockerfile:1.7

FROM alpine:3.22@sha256:14358309a308569c32bdc37e2e0e9694be33a9d99e68afb0f5ff33cc1f695dce AS audio-build
RUN apk add --no-cache build-base nasm pkgconf
ADD --chmod=644 --checksum=sha256:464beb5e7bf0c311e68b45ae2f04e9cc2af88851abb4082231742a74d97b524c \
    https://ffmpeg.org/releases/ffmpeg-8.1.2.tar.xz /sources/ffmpeg-8.1.2.tar.xz
COPY packaging/server/build-ffmpeg.sh /sources/build-ffmpeg.sh
ENV SOURCE_DATE_EPOCH=0
RUN mkdir -p /src/ffmpeg \
 && tar -xJf /sources/ffmpeg-8.1.2.tar.xz --strip-components=1 -C /src/ffmpeg \
 && JOBS=8 sh /sources/build-ffmpeg.sh /src/ffmpeg /opt/ffmpeg

FROM python:3.12-alpine3.22@sha256:a190708a2dec1bd18b1decb539f8e8f5407abaa9bf39cacda583f7f8c11db322 AS python-deps
RUN apk add --no-cache build-base libffi-dev
COPY packaging/server/requirements-airplay-build.txt packaging/server/requirements-airplay.txt /requirements/
RUN python -m pip install --no-cache-dir --require-hashes -r /requirements/requirements-airplay-build.txt \
 && python -m pip wheel --no-cache-dir --no-build-isolation --no-deps --require-hashes \
    -r /requirements/requirements-airplay.txt --wheel-dir /wheels

FROM python:3.12-alpine3.22@sha256:a190708a2dec1bd18b1decb539f8e8f5407abaa9bf39cacda583f7f8c11db322 AS python-sources
COPY packaging/server/fetch-python-sources.py packaging/server/requirements-airplay.txt /build/
RUN python /build/fetch-python-sources.py /build/requirements-airplay.txt /sources/python

FROM python:3.12-alpine3.22@sha256:a190708a2dec1bd18b1decb539f8e8f5407abaa9bf39cacda583f7f8c11db322 AS media-runtime
RUN apk add --no-cache libffi libgcc
RUN --mount=type=bind,from=python-deps,source=/wheels,target=/wheels \
    python -m pip install --no-cache-dir --no-index --no-deps /wheels/*.whl \
 && python -m pip check
COPY --from=audio-build /opt/ffmpeg/bin/ffmpeg /usr/local/bin/ffmpeg
COPY --from=audio-build /sources/ /usr/share/jastreamer/sources/
COPY --from=audio-build /src/ffmpeg/COPYING.LGPLv2.1 /usr/share/licenses/ffmpeg/COPYING.LGPLv2.1
COPY --from=python-sources /sources/python/ /usr/share/jastreamer/sources/python/
COPY packaging/server/fetch-python-sources.py packaging/server/requirements-airplay-build.txt /usr/share/jastreamer/sources/
COPY packaging/server/requirements-airplay.txt /usr/share/jastreamer/requirements-airplay.txt
COPY packaging/server/THIRD-PARTY-NOTICES.txt /usr/share/jastreamer/THIRD-PARTY-NOTICES.txt

FROM --platform=$BUILDPLATFORM node:22.20.0-alpine3.22@sha256:dbcedd8aeab47fbc0f4dd4bffa55b7c3c729a707875968d467aaaea42d6225af AS web-build
WORKDIR /src/apps/control
COPY apps/control/package.json apps/control/package-lock.json ./
RUN npm ci
COPY apps/control/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.25-alpine@sha256:1ae0735f00daffa3aaf1363a5184c0d2dc55c78e3db4ec70241cdac97bf84b59 AS server-build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=0.2.0
ARG REVISION
RUN test "$VERSION" = "0.2.0" && test -n "$REVISION"
WORKDIR /src
COPY apps/server/go.mod apps/server/go.sum ./apps/server/
RUN cd apps/server && go mod download
RUN mkdir -p /out/third-party-licenses/go \
 && cp /usr/local/go/LICENSE /out/third-party-licenses/go/go-runtime.txt \
 && cp /go/pkg/mod/github.com/enbility/zeroconf/v3@v3.0.0-20260410070239-bf752832c353/LICENSE /out/third-party-licenses/go/enbility-zeroconf.txt \
 && cp /go/pkg/mod/github.com/google/uuid@v1.6.0/LICENSE /out/third-party-licenses/go/google-uuid.txt \
 && cp /go/pkg/mod/github.com/miekg/dns@v1.1.62/LICENSE /out/third-party-licenses/go/miekg-dns.txt \
 && cp /go/pkg/mod/github.com/dustin/go-humanize@v1.0.1/LICENSE /out/third-party-licenses/go/dustin-go-humanize.txt \
 && cp /go/pkg/mod/github.com/mattn/go-isatty@v0.0.24/LICENSE /out/third-party-licenses/go/mattn-go-isatty.txt \
 && cp /go/pkg/mod/github.com/ncruces/go-strftime@v1.0.0/LICENSE /out/third-party-licenses/go/ncruces-go-strftime.txt \
 && cp /go/pkg/mod/github.com/remyoudompheng/bigfft@v0.0.0-20230129092748-24d4a6f8daec/LICENSE /out/third-party-licenses/go/remyoudompheng-bigfft.txt \
 && cp /go/pkg/mod/golang.org/x/crypto@v0.55.0/LICENSE /out/third-party-licenses/go/golang-x.txt \
 && cp /go/pkg/mod/modernc.org/libc@v1.74.4/LICENSE /out/third-party-licenses/go/modernc-libc.txt \
 && cp /go/pkg/mod/modernc.org/libc@v1.74.4/LICENSE-3RD-PARTY.md /out/third-party-licenses/go/modernc-libc-third-party.txt \
 && cp /go/pkg/mod/modernc.org/mathutil@v1.7.1/LICENSE /out/third-party-licenses/go/modernc-mathutil.txt \
 && cp /go/pkg/mod/modernc.org/memory@v1.11.0/LICENSE /out/third-party-licenses/go/modernc-memory.txt \
 && cp /go/pkg/mod/modernc.org/memory@v1.11.0/LICENSE-GO /out/third-party-licenses/go/modernc-memory-go.txt \
 && cp /go/pkg/mod/modernc.org/memory@v1.11.0/LICENSE-MMAP-GO /out/third-party-licenses/go/modernc-memory-mmap-go.txt \
 && cp /go/pkg/mod/modernc.org/sqlite@v1.57.0/LICENSE /out/third-party-licenses/go/modernc-sqlite.txt \
 && cp /go/pkg/mod/modernc.org/sqlite@v1.57.0/LICENSE-SQLITE /out/third-party-licenses/go/sqlite-public-domain.txt \
 && cp /go/pkg/mod/modernc.org/sqlite@v1.57.0/LICENSE-SQLITE_VEC /out/third-party-licenses/go/sqlite-vec.txt
COPY apps/server/ ./apps/server/
COPY --from=web-build /src/apps/server/web/ui/dist ./apps/server/web/ui/dist
RUN cd apps/server && CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
    -ldflags="-s -w -X main.productVersion=$VERSION -X main.sourceRevision=$REVISION" \
    -o /out/jastreamer-server ./cmd/jastreamer-server
ARG CREATED
RUN test -n "$CREATED" \
 && addgroup -S -g 10001 jastreamer \
 && adduser -S -D -H -u 10001 -G jastreamer jastreamer \
 && mkdir -p /etc/jastreamer /var/lib/jastreamer /music \
 && chown -R 10001:10001 /etc/jastreamer /var/lib/jastreamer

FROM media-runtime
ARG VERSION=0.2.0
ARG REVISION
ARG CREATED
LABEL org.opencontainers.image.title="jastreamer Server" \
      org.opencontainers.image.description="Self-hosted Web music library with UPnP/DLNA and AirPlay outputs" \
      org.opencontainers.image.vendor="jastreamer" \
      org.opencontainers.image.version="$VERSION" \
      org.opencontainers.image.revision="$REVISION" \
      org.opencontainers.image.created="$CREATED" \
      org.opencontainers.image.source="https://github.com/furyheimdall/jastreamer" \
      org.opencontainers.image.licenses="Apache-2.0"
COPY --from=server-build /etc/passwd /etc/group /etc/
COPY --from=server-build --chown=10001:10001 /etc/jastreamer /etc/jastreamer
COPY --chown=10001:10001 packaging/server/server.json /etc/jastreamer/server.json
COPY --from=server-build --chown=10001:10001 /var/lib/jastreamer /var/lib/jastreamer
COPY --from=server-build /music /music
COPY --from=server-build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=server-build /out/jastreamer-server /usr/local/bin/jastreamer-server
COPY apps/server/internal/airplay/helper.py /usr/local/lib/jastreamer/airplay-helper.py
COPY --chmod=755 packaging/server/jastreamer-airplay /usr/local/bin/jastreamer-airplay
ENV PYTHONDONTWRITEBYTECODE=1
COPY --from=server-build /out/third-party-licenses/ /usr/share/licenses/jastreamer-third-party/
COPY --from=web-build /src/apps/control/node_modules/react/LICENSE /usr/share/licenses/jastreamer-third-party/web/react-react-dom-scheduler.txt
COPY LICENSE /usr/share/licenses/jastreamer/LICENSE
ENV JASTREAMER_CONFIG=/etc/jastreamer/server.json
VOLUME ["/etc/jastreamer", "/var/lib/jastreamer"]
USER 10001:10001
WORKDIR /var/lib/jastreamer
EXPOSE 8080 8443
ENTRYPOINT ["/usr/local/bin/jastreamer-server"]
CMD ["--config", "/etc/jastreamer/server.json"]
