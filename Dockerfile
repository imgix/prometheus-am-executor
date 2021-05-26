FROM docker.io/s390x/golang:1.15 AS builder
WORKDIR /go/src/github.com/imgix/prometheus-am-executor
COPY . .
RUN go build

FROM docker.io/s390x/ubuntu:devel
COPY --from=builder /go/src/github.com/imgix/prometheus-am-executor/prometheus-am-executor /usr/bin/
ENTRYPOINT ["/usr/bin/operator", "-f", "examples/executor.yml"]