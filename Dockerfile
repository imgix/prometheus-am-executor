FROM golang:1.8-alpine as builder

WORKDIR /go/src/github.com/prom-am-executor
ADD . /go/src/github.com/prom-am-executor

RUN apk --no-cache add git \
  && go get -u github.com/prometheus/client_golang/prometheus \
  && go get -u github.com/prometheus/alertmanager/template

RUN CGO_ENABLED=1 GOOS=linux go build -o prom-am-executor /go/src/github.com/prom-am-executor/main.go

FROM alpine:3.7
WORKDIR /app
COPY --from=builder /go/src/github.com/prom-am-executor /app/
ENTRYPOINT ["./prom-am-executor"]
