# Build stage
FROM golang:1.22-alpine AS builder

# Install build dependencies:
#   gcc, musl-dev, linux-headers: required for CGO (go-ble HCI backend)
#   bluez-dev: BlueZ development headers
#   upx: binary compression
RUN apk add --no-cache git gcc musl-dev linux-headers bluez-dev upx

WORKDIR /app
COPY src/ .
RUN go mod download && go mod tidy

# Build with CGO enabled (required for go-ble HCI backend)
RUN CGO_ENABLED=1 GOOS=linux go build \
    -ldflags="-s -w -linkmode external -extldflags '-static'" \
    -trimpath -o ruuvihome .
RUN upx --best --lzma ruuvihome

# Runtime stage - scratch for minimal image size (static binary)
FROM scratch
COPY --from=builder /app/ruuvihome /ruuvihome
ENTRYPOINT ["/ruuvihome"]
CMD ["-config", "/config.yml"]
