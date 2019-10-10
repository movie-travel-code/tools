ARG VERSION=1.12-alpine
FROM golang:${VERSION}

ENV CGO_ENABLED=0
ENV GO111MODULE=off

RUN apk add --no-cache --quiet make curl git jq unzip tree && \
     apk add bash && \
     go get -t golang.org/x/sync/errgroup && \
     go get -t golang.org/x/xerrors && \
     go get -t github.com/hashicorp/golang-lru && \
     go get -t honnef.co/go/tools/simple && \
     go get -t honnef.co/go/tools/staticcheck && \
     go get -t honnef.co/go/tools/stylecheck

VOLUME /go/src/golang.org/x/tools
WORKDIR /go/src/golang.org/x/tools
