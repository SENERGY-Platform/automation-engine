# the device simulator of the local Docker Compose setup; built from the repository root
FROM golang:1.27 AS builder
COPY . /go/src/app
WORKDIR /go/src/app
RUN CGO_ENABLED=0 go build -o devicesim ./tools/devicesim

FROM alpine:3.22
COPY --from=builder /go/src/app/devicesim /devicesim
ENTRYPOINT ["/devicesim"]
