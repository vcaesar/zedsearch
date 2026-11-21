# syntax=docker/dockerfile:1
############################
# STEP 1 build frontend dist
############################
FROM node:24-slim AS frontend
WORKDIR /frontend
COPY ./frontend /frontend/

RUN npm ci
RUN npm run build

############################
# STEP 2 build executable binary
############################
# FROM golang:alpine AS builder
FROM golang:1.27 AS builder
ARG VERSION
ARG COMMIT_HASH
ARG BUILD_DATE

RUN update-ca-certificates
# RUN apk update && apk add --no-cache git
# Create zedsearch user.
ENV USER=zedsearch
ENV GROUP=zedsearch
ENV UID=10001
ENV GID=10001
# See https://stackoverflow.com/a/55757473/12429735RUN
RUN groupadd --gid "${GID}" "${GROUP}"
RUN adduser \
    --disabled-password \
    --gecos "" \
    --home "/nonexistent" \
    --shell "/sbin/nologin" \
    --no-create-home \
    --uid "${UID}" \
    --gid "${GID}" \
    "${USER}"
# Create default directories for persistent ZedSearch data used in final build stage.
# It follows the Linux filesystem hierarchy pattern
# https://tldp.org/LDP/Linux-Filesystem-Hierarchy/html/var.html
RUN mkdir -p /var/lib/zedsearch /data /tmp && chown zedsearch:zedsearch /var/lib/zedsearch /data /tmp
WORKDIR $GOPATH/src/github.com/vcaesar/zedsearch/
# Fetch dependencies in their own layer so source edits keep the module cache.
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY . .
COPY --from=frontend /frontend/dist frontend/dist

ENV VERSION=$VERSION
ENV COMMIT_HASH=$COMMIT_HASH
ENV BUILD_DATE=$BUILD_DATE

# `web` embeds frontend/dist. No `ne`: keep gse's big dict so zed_plugin_gse_dict_embed=big indexes tokenize the same after upgrade.
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -tags web -ldflags="-s -w -X github.com/vcaesar/zedsearch/pkg/meta.Version=${VERSION} -X github.com/vcaesar/zedsearch/pkg/meta.CommitHash=${COMMIT_HASH} -X github.com/vcaesar/zedsearch/pkg/meta.BuildDate=${BUILD_DATE}" -o zedsearch cmd/zedsearch/main.go
############################
# STEP 3 build a small image
############################
# FROM public.ecr.aws/lts/ubuntu:latest
FROM scratch

# Import the user and group files from the builder.
COPY --from=builder /etc/passwd /etc/passwd
COPY --from=builder /etc/group /etc/group

# scratch has neither: the CA bundle is for outbound TLS (S3/GCS/OSS, etcd, OTLP,
# Sentry), zoneinfo for named time zones ("time_zone": "Asia/Shanghai").
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo

# Copy our static executable.
COPY --from=builder  /go/src/github.com/vcaesar/zedsearch/zedsearch /go/bin/zedsearch

# Create directories that can be used to keep ZedSearch data persistent along with host source or named volumes
COPY --from=builder --chown=zedsearch:zedsearch /var/lib/zedsearch /var/lib/zedsearch
COPY --from=builder --chown=zedsearch:zedsearch /data /data
# os.TempDir: object storage spools segments here when zed_object_cache_size = 0.
COPY --from=builder --chown=zedsearch:zedsearch /tmp /tmp

# Unprivileged, numeric so Kubernetes runAsNonRoot can verify it.
USER 10001:10001
# Port on which the service will be exposed.
EXPOSE 4080
# Run the zedsearch binary.
ENTRYPOINT ["/go/bin/zedsearch"]
