FROM golang:1.26.10-alpine3.24@sha256:a2012b4a1d1f9f7e9f44e0d39b805a901e856b1dd80b4e6ee4338db547050308 AS golang

RUN apk add --no-cache -U \
    curl git

WORKDIR /usr/local
# hadolint ignore=DL4006
RUN curl -sL https://taskfile.dev/install.sh | sh

WORKDIR /go/src/semaphore
COPY . /go/src/semaphore

RUN --mount=type=cache,target=/go/pkg \
    go mod download -x

RUN --mount=type=cache,target=/go/pkg --mount=type=cache,target=/root/.cache/go-build \
    task deps:tools && \
    task deps:be && \
    task dredd:goodman && \
    task dredd:hooks

FROM apiaryio/dredd:14.0.0

RUN apk add --no-cache -U \
    bash git go

COPY --from=golang /go/bin/goodman /root/go/bin/goodman
COPY --from=golang /go/src/semaphore /semaphore
WORKDIR /semaphore

COPY deployment/docker/dredd/entrypoint /usr/local/bin
ENTRYPOINT ["/usr/local/bin/entrypoint"]
