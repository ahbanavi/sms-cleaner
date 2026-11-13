# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
ARG TARGETOS TARGETARCH VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Pure-Go SQLite, so cross-compiling for the Orange Pi (arm64) needs no C toolchain.
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/sms-cleaner .

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata \
 && adduser -D -H -u 1000 app \
 && mkdir /data && chown app /data
COPY --from=build /out/sms-cleaner /usr/local/bin/sms-cleaner
USER app
ENV SMSC_DATA_DIR=/data SMSC_LISTEN=:8080
VOLUME /data
EXPOSE 8080
HEALTHCHECK --interval=1m --timeout=5s CMD wget -qO- http://127.0.0.1:8080/healthz >/dev/null || exit 1
ENTRYPOINT ["/usr/local/bin/sms-cleaner"]
CMD ["serve"]
