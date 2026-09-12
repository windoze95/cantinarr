FROM golang:1.25-alpine AS builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags "-X github.com/windoze95/cantinarr-server/internal/version.Version=${VERSION}" -o cantinarr ./cmd/server

FROM --platform=$BUILDPLATFORM alpine:3.22 AS codex-downloader
ARG TARGETARCH
ARG CODEX_VERSION=0.144.3
RUN apk add --no-cache ca-certificates curl tar
RUN set -eux; \
    case "$TARGETARCH" in \
      amd64) target="x86_64-unknown-linux-musl"; sha="6fa4467489ac5a0ae5bf0057d39f6d14a7f50f5fa70b8a10933888d92d1b75ab" ;; \
      arm64) target="aarch64-unknown-linux-musl"; sha="a70aed45f237e336f266e39c036f6f9a91ec181dacddf5d21f7f6d0d34b5c654" ;; \
      *) echo "unsupported Codex architecture: $TARGETARCH" >&2; exit 1 ;; \
    esac; \
    archive="codex-app-server-${target}.tar.gz"; \
    curl --fail --location --retry 3 \
      "https://github.com/openai/codex/releases/download/rust-v${CODEX_VERSION}/${archive}" \
      --output "/tmp/${archive}"; \
    echo "${sha}  /tmp/${archive}" | sha256sum -c -; \
    tar -xzf "/tmp/${archive}" -C /tmp; \
    install -m 0755 "/tmp/codex-app-server-${target}" /codex-app-server; \
    curl --fail --location --retry 3 \
      "https://raw.githubusercontent.com/openai/codex/rust-v${CODEX_VERSION}/LICENSE" \
      --output /tmp/CODEX-LICENSE; \
    curl --fail --location --retry 3 \
      "https://raw.githubusercontent.com/openai/codex/rust-v${CODEX_VERSION}/NOTICE" \
      --output /tmp/CODEX-NOTICE; \
    echo "d17f227e4df5da1600391338865ce0f3055211760a36688f816941d58232d8dc  /tmp/CODEX-LICENSE" | sha256sum -c -; \
    echo "9d71575ecfd9a843fc1677b0efb08053c6ba9fd686a0de1a6f5382fd3c220915  /tmp/CODEX-NOTICE" | sha256sum -c -; \
    install -d /codex-license; \
    install -m 0644 /tmp/CODEX-LICENSE /codex-license/LICENSE; \
    install -m 0644 /tmp/CODEX-NOTICE /codex-license/NOTICE

# Companion runs in a private stdio worker, with a pinned Python dependency set.
FROM alpine:3.19 AS appletv-builder
RUN apk add --no-cache python3 py3-pip libstdc++ build-base python3-dev libffi-dev
RUN python3 -m venv /opt/cantinarr-appletv
COPY tools/apple_tv/requirements.lock /tmp/apple-tv-requirements.lock
RUN /opt/cantinarr-appletv/bin/pip install --no-cache-dir --require-hashes -r /tmp/apple-tv-requirements.lock
# Keep package metadata and bundled license files in the venv. An index lets
# operators find each distribution's notices without relying on network access.
COPY tools/apple_tv/collect_notices.py /tmp/collect_notices.py
RUN /opt/cantinarr-appletv/bin/python /tmp/collect_notices.py /apple-tv-notices

FROM alpine:3.19
# su-exec is what the entrypoint drops privileges with when PUID/PGID are set.
RUN apk add --no-cache ca-certificates su-exec python3 libstdc++
COPY --from=builder /build/cantinarr /usr/local/bin/
COPY --from=codex-downloader /codex-app-server /usr/local/bin/
COPY --from=codex-downloader /codex-license/ /usr/share/licenses/codex-app-server/
COPY --from=appletv-builder /opt/cantinarr-appletv/ /opt/cantinarr-appletv/
COPY --from=appletv-builder /apple-tv-notices/ /usr/share/licenses/cantinarr-appletv/
COPY tools/apple_tv/cantinarr_appletv.py /usr/lib/cantinarr/apple_tv/cantinarr_appletv.py
COPY --chmod=0755 tools/apple_tv/helper.sh /usr/local/bin/cantinarr-appletv-helper
RUN cantinarr-appletv-helper --self-test
# Shared with the root Dockerfile: honours PUID/PGID (own /config, run as that
# user), otherwise exec's the command untouched, root as before.
COPY --chmod=0755 docker/entrypoint.sh /entrypoint.sh
EXPOSE 8585
VOLUME /config
ENTRYPOINT ["/entrypoint.sh"]
CMD ["cantinarr"]
