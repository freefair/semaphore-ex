# The Go toolchain must match the build platform: a bare COPY --from=<image> resolves the
# image for the *target* platform, so cross-building linux/arm64 on an amd64 runner copied
# an arm64 Go that ran under emulation and failed to assemble cgo with the host gcc.
FROM --platform=$BUILDPLATFORM golang:1.26.8-alpine3.24@sha256:ce864e7223ac17b1775e6fd0b4c0db580c2eb50e7953a427916379e4b92a1628 AS goroot

FROM --platform=$BUILDPLATFORM node:24.19.0-alpine3.24@sha256:d32cdf619f63fe0471182d08996dd516c6275bb5fd31ae06e55a570bd9e1ad43 AS builder

COPY --from=goroot /usr/local/go /usr/local/go

ENV GOPATH=/go
ENV PATH=/usr/local/go/bin:/go/bin:$PATH

ARG TASK_VERSION=3.53.1

RUN apk add --no-cache -U \
    libc-dev curl git gcc zip unzip tar && \
    go install github.com/go-task/task/v3/cmd/task@v${TASK_VERSION}

WORKDIR /go/src/semaphore
COPY . /go/src/semaphore

RUN --mount=type=cache,target=/go/pkg \
    go mod download -x

ARG TARGETOS
ARG TARGETARCH
ARG CORE_REVISION
ARG ENHANCED_REVISION

RUN --mount=type=cache,target=/go/pkg \
    --mount=type=cache,target=/root/.cache/go-build \
    task deps:be && \
    task deps:fe && \
    task build GOOS=${TARGETOS} GOARCH=${TARGETARCH} CORE_REVISION=${CORE_REVISION} ENHANCED_REVISION=${ENHANCED_REVISION}


# renovate: datasource=github-releases depName=opentofu/opentofu
ENV OPENTOFU_VERSION=1.11.0
# renovate: datasource=github-releases depName=hashicorp/terraform
ENV TERRAFORM_VERSION=1.11.3
# renovate: datasource=github-releases depName=gruntwork-io/terragrunt
ENV TERRAGRUNT_VERSION=0.78.0
#ENV PULUMI_VERSION="3.116.1"

RUN wget https://github.com/opentofu/opentofu/releases/download/v${OPENTOFU_VERSION}/tofu_${OPENTOFU_VERSION}_linux_${TARGETARCH}.tar.gz && \
    case "${TARGETARCH}" in amd64) checksum=2463ce749bbe7396e7a384918e1065aeda54317e3fec8db07c2bddee8f714c61 ;; arm64) checksum=cd0b275699416f1417fed4d2911e557eef78126a545fcfc0cb5eee68fe192524 ;; *) exit 1 ;; esac && \
    echo "${checksum}  tofu_${OPENTOFU_VERSION}_linux_${TARGETARCH}.tar.gz" | sha256sum -c - && \
    tar xf tofu_${OPENTOFU_VERSION}_linux_${TARGETARCH}.tar.gz -C /tmp && \
    rm tofu_${OPENTOFU_VERSION}_linux_${TARGETARCH}.tar.gz

RUN curl -fSLO https://releases.hashicorp.com/terraform/${TERRAFORM_VERSION}/terraform_${TERRAFORM_VERSION}_linux_${TARGETARCH}.zip && \
    case "${TARGETARCH}" in amd64) checksum=377c8c18e2beab24f721994859236e98383350bf767921436511370d1f7c472b ;; arm64) checksum=d685953bec501c0acda13319f34dddaadf33a8f553c85533531d3c7d5f84604a ;; *) exit 1 ;; esac && \
    echo "${checksum}  terraform_${TERRAFORM_VERSION}_linux_${TARGETARCH}.zip" | sha256sum -c - && \
    unzip terraform_${TERRAFORM_VERSION}_linux_${TARGETARCH}.zip -d /tmp && \
    rm terraform_${TERRAFORM_VERSION}_linux_${TARGETARCH}.zip

RUN wget -O /tmp/terragrunt https://github.com/gruntwork-io/terragrunt/releases/download/v${TERRAGRUNT_VERSION}/terragrunt_linux_${TARGETARCH} && \
    case "${TARGETARCH}" in amd64) checksum=debc5b37c44f2e170f83995c3ff8068cfcc61f32e93c28ea442555b56de93c46 ;; arm64) checksum=1d4ca5e428325a4d76ed2d9dbb4f9fb3acce8be781442a184aa3dd3236076cb8 ;; *) exit 1 ;; esac && \
    echo "${checksum}  /tmp/terragrunt" | sha256sum -c - && \
    chmod +x /tmp/terragrunt

FROM alpine:3.24@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b

ARG TARGETARCH="amd64"
ARG CORE_REVISION
ARG ENHANCED_REVISION

LABEL org.opencontainers.image.revision=${CORE_REVISION} \
      io.semaphore.edition=enhanced \
      io.semaphore.core.revision=${CORE_REVISION} \
      io.semaphore.enhanced.revision=${ENHANCED_REVISION}

RUN apk add --no-cache -U \
    bash curl git gnupg mysql-client openssh-client-default python3 python3-dev py3-pip rsync sshpass tar tini tzdata unzip wget zip && \
    rm -rf /var/cache/apk/* && \
    adduser -D -u 1001 -G root semaphore && \
    mkdir -p /tmp/semaphore && \
    mkdir -p /etc/semaphore && \
    mkdir -p /var/lib/semaphore && \
    mkdir -p /opt/semaphore && \
    chown -R semaphore:0 /tmp/semaphore && \
    chown -R semaphore:0 /etc/semaphore && \
    chown -R semaphore:0 /var/lib/semaphore && \
    chown -R semaphore:0 /opt/semaphore && \
    find /usr/lib/python* -iname __pycache__ | xargs rm -rf

COPY --chown=1001:0 ./deployment/docker/runner/ansible.cfg /etc/ansible/ansible.cfg
COPY --from=builder /go/src/semaphore/deployment/docker/runner/runner-wrapper /usr/local/bin/
COPY --from=builder /go/src/semaphore/bin/semaphore /usr/local/bin/
COPY --from=builder /tmp/tofu /usr/local/bin/
COPY --from=builder /tmp/terraform /usr/local/bin/
COPY --from=builder /tmp/terragrunt /usr/local/bin/

RUN chown -R semaphore:0 /usr/local/bin/runner-wrapper && \
    chmod +x /usr/local/bin/runner-wrapper && \
    chown -R semaphore:0 /usr/local/bin/semaphore && \
    chmod +x /usr/local/bin/semaphore

WORKDIR /home/semaphore

# renovate: datasource=pypi depName=ansible
ARG ANSIBLE_VERSION=13.5.0
ENV ANSIBLE_VERSION=${ANSIBLE_VERSION}
ARG ANSIBLE_VENV_PATH=/opt/semaphore/apps/ansible/${ANSIBLE_VERSION}/venv

RUN apk add --no-cache -U python3-dev build-base openssl-dev libffi-dev cargo && \
     mkdir -p ${ANSIBLE_VENV_PATH} && \
     python3 -m venv ${ANSIBLE_VENV_PATH} --system-site-packages && \
     source ${ANSIBLE_VENV_PATH}/bin/activate && \
     pip3 install --upgrade pip ansible==${ANSIBLE_VERSION} boto3 botocore requests pywinrm passlib && \
     apk del python3-dev build-base openssl-dev libffi-dev cargo && \
     rm -rf /var/cache/apk/* && \
     find ${ANSIBLE_VENV_PATH} -iname __pycache__ | xargs rm -rf && \
     chown -R semaphore:0 /opt/semaphore

RUN echo 'Host *\n  StrictHostKeyChecking no\n  UserKnownHostsFile /dev/null' > /etc/ssh/ssh_config.d/semaphore.conf

USER 1001

ENV VIRTUAL_ENV="$ANSIBLE_VENV_PATH"
ENV PATH="$ANSIBLE_VENV_PATH/bin:$PATH"

# Preventing ansible zombie processes. Tini kills zombies.
ENTRYPOINT ["/sbin/tini", "--"]
CMD [ "/usr/local/bin/runner-wrapper"]
