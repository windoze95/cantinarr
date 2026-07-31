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

FROM alpine:3.19
RUN apk add --no-cache ca-certificates
COPY --from=builder /build/cantinarr /usr/local/bin/
COPY --from=codex-downloader /codex-app-server /usr/local/bin/
COPY --from=codex-downloader /codex-license/ /usr/share/licenses/codex-app-server/
EXPOSE 8585
VOLUME /config
CMD ["cantinarr"]
