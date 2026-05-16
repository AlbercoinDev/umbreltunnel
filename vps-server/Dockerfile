FROM golang:1.21-alpine AS builder

WORKDIR /build
COPY go.mod .
COPY main.go .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o vps-server .

FROM alpine:3.19

RUN apk add --no-cache ca-certificates curl wireguard-tools docker-cli

COPY --from=builder /build/vps-server /usr/local/bin/vps-server

EXPOSE 8081

ENTRYPOINT ["vps-server"]
