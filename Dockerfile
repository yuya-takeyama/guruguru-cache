FROM golang:1.11-alpine3.8 as builder

RUN apk --update add gcc

WORKDIR /go/src/github.com/yuya-takeyama/guruguru-cache
COPY . /go/src/github.com/yuya-takeyama/guruguru-cache

RUN CGO_ENABLED=0 GOOS=linux go build -a -ldflags '-extldflags "-static"'

FROM busybox

COPY --from=builder /go/src/github.com/yuya-takeyama/guruguru-cache/guruguru-cache /usr/local/bin/guruguru-cache

ENTRYPOINT ["/usr/local/bin/guruguru-cache"]
