FROM golang:1.21-alpine AS builder

WORKDIR /build
COPY go.mod .
COPY main.go .
COPY internal/ ./internal/
COPY web/ ./web/

RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o umbrel-tunnel .

FROM alpine:3.19

RUN apk add --no-cache \
    wireguard-tools \
    iptables \
    iproute2 \
    ca-certificates \
    openresolv \
    bash

RUN mkdir -p /data /etc/wireguard && chmod 700 /data

COPY --from=builder /build/umbrel-tunnel /usr/local/bin/umbrel-tunnel

EXPOSE 8080

VOLUME ["/data"]

ENTRYPOINT ["umbrel-tunnel"]
