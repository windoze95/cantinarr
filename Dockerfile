# Stage 1: Build Flutter web
FROM ghcr.io/cirruslabs/flutter:stable AS flutter-builder
WORKDIR /app
COPY app/ .
RUN flutter pub get && flutter build web --release

# Stage 2: Build Go binary
FROM golang:1.25-alpine AS go-builder
WORKDIR /build
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ .
# Copy Flutter web build into the Go embed directory
COPY --from=flutter-builder /app/build/web/ ./internal/web/dist/
RUN CGO_ENABLED=0 go build -o cantinarr ./cmd/server

# Stage 3: Final image
FROM alpine:3.19
RUN apk add --no-cache ca-certificates
COPY --from=go-builder /build/cantinarr /usr/local/bin/
EXPOSE 8585
VOLUME /config
CMD ["cantinarr"]
