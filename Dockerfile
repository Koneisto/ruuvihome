# Build stage
FROM golang:1.27.2-alpine@sha256:f92b6ef800e499660581efdabdf25d9d817a9d124eaf900924f0504e7e27e12d AS builder
RUN apk add --no-cache git upx
WORKDIR /app
COPY src/ .
RUN go mod download && go mod verify
# Pure Go build - no CGO needed (D-Bus via godbus)
RUN CGO_ENABLED=0 GOOS=linux go build -mod=readonly \
    -ldflags="-s -w" \
    -trimpath -o ruuvihome .
RUN upx --best --lzma ruuvihome

# Runtime stage - scratch (static binary, D-Bus socket mounted from host)
FROM scratch
COPY --from=builder /app/ruuvihome /ruuvihome
ENTRYPOINT ["/ruuvihome"]
CMD ["-config", "/config.yml"]
