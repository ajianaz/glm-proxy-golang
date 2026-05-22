# Build stage
FROM golang:1.25-alpine AS builder

# Build-time version (injected by CI)
ARG VERSION=dev

WORKDIR /build

# Install tzdata for timezone support in scratch image
RUN apk add --no-cache tzdata

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build static binary with version injected via ldflags
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w -X main.Version=${VERSION}" \
    -o /server ./cmd/server

# Runtime stage
FROM scratch

# CA certificates for HTTPS upstream calls
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
# Timezone data (installed via apk in builder)
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo

COPY --from=builder /server /server

EXPOSE 3000

ENTRYPOINT ["/server"]
