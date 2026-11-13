FROM golang:1.26.11-alpine3.24@sha256:ae52b5bc6ed045a9ed26ab03dc19327a802a9cc9aa8b27ed57f5c6b60579b0bb AS golang

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
