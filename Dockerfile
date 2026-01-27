# Build stage
FROM golang:1.22-alpine AS builder
RUN apk add --no-cache git gcc musl-dev linux-headers upx
WORKDIR /app
COPY src/ .
RUN go mod download && go mod tidy
# Build with static linking for musl
RUN CGO_ENABLED=1 GOOS=linux go build \
    -ldflags="-s -w -linkmode external -extldflags '-static'" \
    -trimpath -o ruuvihome .
RUN upx --best --lzma ruuvihome

# Runtime stage - scratch for minimal size (static binary)
FROM scratch
COPY --from=builder /app/ruuvihome /ruuvihome
ENTRYPOINT ["/ruuvihome"]
CMD ["-config", "/config.yml"]
