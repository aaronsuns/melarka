# syntax=mirror.gcr.io/docker/dockerfile:1@sha256:4edf897a3ffa55b89f906fc8cc78afdb3f1834cc9c7083565e611a8a7d5fe99e
# The Dockerfile frontend (above) and the base images: Docker's own images through Google's Docker Hub mirror (mirror.gcr.io), which has no
# anonymous pull limit (Docker Hub answers 429 when several CI builds start at once). Pinned to the
# multi-arch index digest, which is the same on Docker Hub: the bytes are Docker's, and a build is repeatable.
FROM --platform=$BUILDPLATFORM mirror.gcr.io/library/node:26-alpine@sha256:6f08a523443ddedf52941874c203475b0ad503e859ad70cb26ab8be9a517dbd5 AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM mirror.gcr.io/library/golang:1.26-alpine@sha256:27f2d3b83cfc5803c8078f8e730af0f7c431bf067afaec8f60a36fb763187cba AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /web/dist/ ./internal/webui/dist/
ARG TARGETOS TARGETARCH VERSION=dev
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
    -ldflags="-s -w -X github.com/aaronsuns/lark-server/internal/buildinfo.Version=${VERSION}" -o /out/lark ./cmd/lark

FROM mirror.gcr.io/library/alpine:3.24@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6
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
