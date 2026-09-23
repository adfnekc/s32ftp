# Build stage
FROM golang:1.25-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

ARG VERSION=dev
RUN CGO_ENABLED=0 go build \
    -trimpath \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/s32ftp ./cmd/s32ftp

# Runtime stage
FROM alpine:3.20

RUN apk add --no-cache ca-certificates \
    && adduser -D -u 10001 -s /sbin/nologin s32ftp

COPY --from=build /out/s32ftp /usr/local/bin/s32ftp
COPY configs/config.example.yaml /etc/s32ftp/config.yaml

USER s32ftp
EXPOSE 2121 50000-50100

ENTRYPOINT ["/usr/local/bin/s32ftp"]
CMD ["-config", "/etc/s32ftp/config.yaml"]
