# the builder runs on the platform of the build host and cross-compiles, so that the arm64 image
# needs no emulation (FOG-02)
FROM --platform=$BUILDPLATFORM golang:1.27 AS builder
ARG TARGETOS=linux
ARG TARGETARCH
# the release tag from prod.yml; the CI checkout carries no tags
ARG VERSION=dev

COPY . /go/src/app
WORKDIR /go/src/app

ENV GO111MODULE=on

RUN go generate ./...
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -o app

RUN echo "$VERSION $(git log -1 --oneline)" > version.txt

FROM alpine:latest
WORKDIR /root/
COPY --from=builder /go/src/app/app .
COPY --from=builder /go/src/app/config.json .
COPY --from=builder /go/src/app/version.txt .

EXPOSE 8080

ENTRYPOINT ["./app"]
