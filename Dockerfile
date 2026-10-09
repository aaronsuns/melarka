# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM node:22-alpine AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /web/dist/ ./internal/webui/dist/
ARG TARGETOS TARGETARCH VERSION=dev
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
    -ldflags="-s -w -X github.com/aaronsuns/lark-server/internal/buildinfo.Version=${VERSION}" -o /out/lark ./cmd/lark

FROM alpine:3.24
ARG VERSION=dev
LABEL org.opencontainers.image.title="Melarka" \
      org.opencontainers.image.source="https://github.com/aaronsuns/melarka" \
      org.opencontainers.image.licenses="AGPL-3.0-or-later" \
      org.opencontainers.image.version="${VERSION}"
# nodejs: the JavaScript runtime yt-dlp needs for YouTube's signature challenges (--js-runtimes node).
RUN apk add --no-cache ffmpeg tzdata ca-certificates nodejs
ARG TARGETARCH
ARG YTDLP_VERSION=2026.07.04
# Pinned per architecture: bump the version and both hashes together (from the release's SHA2-256SUMS).
ARG YTDLP_SHA256_AMD64=f7439ec2e3ffe69e06ac233f83f0d9687b89105939129bddcbf74e5de0f2b40e
ARG YTDLP_SHA256_ARM64=9a6a4de88f35dc68c1763945fbb417e092ebd9afc5d66052ac31b68d405a12a7
RUN set -eux; case "$TARGETARCH" in amd64) f=yt-dlp_musllinux; sum="$YTDLP_SHA256_AMD64" ;; arm64) f=yt-dlp_musllinux_aarch64; sum="$YTDLP_SHA256_ARM64" ;; *) echo "unsupported $TARGETARCH"; exit 1 ;; esac; \
    cd /tmp; wget -qO "$f" "https://github.com/yt-dlp/yt-dlp/releases/download/${YTDLP_VERSION}/$f"; \
    echo "$sum  $f" | sha256sum -c -; \
    install -m 0755 "$f" /usr/local/bin/yt-dlp; rm -f "$f"; /usr/local/bin/yt-dlp --version
COPY --from=build /out/lark /usr/local/bin/lark
# XDG_CACHE_HOME: yt-dlp's cache (player JS, solved challenges) lives on the data volume.
ENV LARK_DATA_DIR=/data LARK_LISTEN=:4600 XDG_CACHE_HOME=/data/cache
EXPOSE 4600
USER 1000:1000
HEALTHCHECK --interval=60s --timeout=5s CMD wget -qO- http://127.0.0.1:4600/ >/dev/null || exit 1
ENTRYPOINT ["/usr/local/bin/lark"]
