FROM golang:alpine as builder
MAINTAINER Martin Baillie <martin.t.baillie@gmail.com>

WORKDIR /go/src/github.com/imgix/prometheus-am-executor
COPY ${PWD} .
RUN apk --no-cache add git \
	&& go get -u github.com/prometheus/alertmanager/... \
        && go get -u github.com/prometheus/client_golang/... \
        && CGO_ENABLED=0 GOARCH=amd64 GOOS=linux \
        go build -a -installsuffix cgo -ldflags '-s -w -extld ld -extldflags -static'

FROM alpine
WORKDIR /
COPY --from=builder /go/src/github.com/imgix/prometheus-am-executor/prometheus-am-executor .
ENTRYPOINT ["/prometheus-am-executor"]
