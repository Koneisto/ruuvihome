# Build stage
FROM golang:1.22-alpine AS builder
RUN apk add --no-cache git upx
WORKDIR /app
COPY src/ .
RUN go mod download && go mod tidy
# Pure Go build - no CGO needed (D-Bus via godbus)
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w" \
    -trimpath -o ruuvihome .
RUN upx --best --lzma ruuvihome

# Runtime stage - scratch (static binary, D-Bus socket mounted from host)
FROM scratch
COPY --from=builder /app/ruuvihome /ruuvihome
ENTRYPOINT ["/ruuvihome"]
CMD ["-config", "/config.yml"]
