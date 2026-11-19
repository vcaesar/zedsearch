# Smallest image: no compilation, the prebuilt (web + ne) binary is downloaded
# from the GitHub release and checked against its checksums.txt.
#   docker buildx build -f docker/Dockerfile.bin --build-arg VERSION=1.2.3 \
#     --platform linux/amd64,linux/arm64,linux/arm/v7 -t <user>/zedsearch:1.2.3 --push .
# Several platforms in one build need the containerd image store or a
# docker-container builder (docker buildx create --use).
FROM --platform=$BUILDPLATFORM alpine:3.22 AS fetch
ARG VERSION
ARG TARGETARCH
ARG TARGETVARIANT
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -g 10001 zedsearch \
    && adduser -D -H -h /nonexistent -s /sbin/nologin -u 10001 -G zedsearch zedsearch \
    && mkdir -p /var/lib/zedsearch /data /tmp \
    && chown zedsearch:zedsearch /var/lib/zedsearch /data /tmp
WORKDIR /dl
# Archive names follow .goreleaser.yml: zedsearch_<version>_linux_{x86_64,arm64,armv6,armv7}.tar.gz
RUN set -eux; \
    : "${VERSION:?set --build-arg VERSION=<release version without v>}"; \
    case "$TARGETARCH" in \
      amd64) arch=x86_64 ;; \
      arm64) arch=arm64 ;; \
      arm) arch="armv${TARGETVARIANT#v}" ;; \
      *) echo "unsupported arch $TARGETARCH" >&2; exit 1 ;; \
    esac; \
    file="zedsearch_${VERSION}_linux_${arch}.tar.gz"; \
    base="https://github.com/vcaesar/zedsearch/releases/download/v${VERSION}"; \
    wget -q -O "$file" "$base/$file"; \
    wget -q -O checksums.txt "$base/checksums.txt"; \
    grep " ${file}\$" checksums.txt | sha256sum -c -; \
    tar -xzf "$file" zedsearch

FROM scratch
ARG VERSION
LABEL org.opencontainers.image.source="https://github.com/vcaesar/zedsearch" \
      org.opencontainers.image.version="${VERSION}"
COPY --from=fetch /etc/passwd /etc/passwd
COPY --from=fetch /etc/group /etc/group
# scratch has neither: the CA bundle is for outbound TLS (S3/GCS/OSS, etcd, OTLP,
# Sentry), zoneinfo for named time zones ("time_zone": "Asia/Shanghai").
COPY --from=fetch /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=fetch /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=fetch --chown=zedsearch:zedsearch /var/lib/zedsearch /var/lib/zedsearch
COPY --from=fetch --chown=zedsearch:zedsearch /data /data
# os.TempDir: object storage spools segments here when zed_object_cache_size = 0.
COPY --from=fetch --chown=zedsearch:zedsearch /tmp /tmp
# The archive's owner is the CI runner (uid 1001); tar and COPY --from keep it.
COPY --from=fetch --chown=0:0 /dl/zedsearch /go/bin/zedsearch
# Numeric, so Kubernetes runAsNonRoot can verify it; same user as zedsearch above.
USER 10001:10001
EXPOSE 4080
ENTRYPOINT ["/go/bin/zedsearch"]
