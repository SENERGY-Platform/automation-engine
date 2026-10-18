# the Keycloak stand-in of the local Docker Compose setup; built from the repository root
FROM golang:1.27 AS builder
COPY . /go/src/app
WORKDIR /go/src/app
RUN CGO_ENABLED=0 go build -o devauth ./tools/devauth

FROM alpine:3.22
COPY --from=builder /go/src/app/devauth /devauth
EXPOSE 8080
ENTRYPOINT ["/devauth"]
