# Installation

ZedSearch has no built-in admin account. Set `ZED_FIRST_ADMIN_USER` and `ZED_FIRST_ADMIN_PASSWORD` the first time you start it; they create the admin user and are not needed on later runs. Change the password after logging in and never commit real credentials.

Data is written to `ZED_DATA_PATH` (default `./data`, created on start). The server listens on port `4080`.

Release binaries and Docker images include the web UI: open [http://localhost:4080](http://localhost:4080) and log in. Builds without the `web` tag are API-only.

## Linux/macOS binary

Download the archive for your platform from the [releases page](https://github.com/vcaesar/zedsearch/releases/latest), extract it, then:

    ZED_FIRST_ADMIN_USER=admin ZED_FIRST_ADMIN_PASSWORD=Complexpass#123 ./zedsearch

`./zedsearch version` prints the version.

## Windows

Download the windows archive from the [releases page](https://github.com/vcaesar/zedsearch/releases/latest), extract it, then in `cmd`:

    set ZED_FIRST_ADMIN_USER=admin
    set ZED_FIRST_ADMIN_PASSWORD=Complexpass#123
    zedsearch.exe

## Docker

Images: `ghcr.io/vcaesar/zedsearch:latest` (or `:<version>`) for tagged releases, `ghcr.io/vcaesar/zedsearch-dev:nightly` for builds from `main`. Platforms: linux/amd64, linux/arm64, linux/arm/v7.

    mkdir data
    docker run -d --name zedsearch -p 4080:4080 \
        -v "$PWD/data:/data" -e ZED_DATA_PATH=/data \
        -e ZED_FIRST_ADMIN_USER=admin -e ZED_FIRST_ADMIN_PASSWORD=Complexpass#123 \
        ghcr.io/vcaesar/zedsearch:latest

The container runs as the non-root user `zedsearch` (uid/gid 10001). If it cannot write to the mounted folder, fix its permissions:

    sudo chown 10001:10001 ./data

The image's working directory is `/`, so a config file can be mounted at `/conf/zed.toml`.

## Kubernetes - Manifest

[k8s/kube-deployment.yaml](https://github.com/vcaesar/zedsearch/blob/main/k8s/kube-deployment.yaml) creates a StatefulSet and a Service `z` in the `zedsearch` namespace. Edit `ZED_FIRST_ADMIN_PASSWORD` in it before applying.

    kubectl create ns zedsearch
    kubectl apply -f https://raw.githubusercontent.com/vcaesar/zedsearch/main/k8s/kube-deployment.yaml
    kubectl -n zedsearch port-forward svc/z 4080:4080

## Kubernetes - Helm

The chart is in [helm/zedsearch](https://github.com/vcaesar/zedsearch/tree/main/helm/zedsearch). Set `auth.username` / `auth.password` in [values.yaml](https://github.com/vcaesar/zedsearch/blob/main/helm/zedsearch/values.yaml), then from a checkout of the repository:

    kubectl create ns zedsearch
    helm install zedsearch helm/zedsearch -n zedsearch
    kubectl -n zedsearch port-forward svc/zedsearch 4080:4080

## Build from source

Requires Go (see `go.mod`); the web UI also needs Node.js 24+ and npm. From the repository root:

```sh
git clone https://github.com/vcaesar/zedsearch.git
cd zedsearch

# API-only binary, no frontend needed
go build -o zedsearch cmd/zedsearch/main.go

# run directly, API-only on :4080
ZED_FIRST_ADMIN_USER=admin ZED_FIRST_ADMIN_PASSWORD=Complexpass#123 go run cmd/zedsearch/main.go

# build the UI, rebuild with -tags web and start; writes test/zedsearch-web
go run cmd/zedsearch/main.go web
```

To build a binary with the UI embedded yourself:

```sh
cd frontend && npm ci && npm run build && cd ..
go build -tags web -o zedsearch cmd/zedsearch/main.go
```

`./sh/build.sh` builds the frontend and a versioned web binary in one step. Add `-tags fiber` (or `-tags web,fiber`) to use fiber instead of gin as the HTTP server.

## Configuration

Settings are applied in this order:

1. Built-in defaults ([pkg/config/default.toml](https://github.com/vcaesar/zedsearch/blob/main/pkg/config/default.toml)).
1. `conf/zed.toml`, relative to the working directory, if present.
1. Non-empty environment variables: every key in uppercase, e.g. `zed_server_port` → `ZED_SERVER_PORT`.

```toml
# conf/zed.toml
zed_first_admin_user = "admin"
zed_first_admin_password = "***"
zed_server_port = "4080"
zed_data_path = "./data"
zed_log_level = "info"

[Shard]
zed_shard_num = 3
```

- List only the keys you change. Grouped keys keep their table header from `default.toml` (`[Cluster]`, `[Tier]`, `[Shard]`, `[Etcd]`, `[S3]`, `[GCS]`, `[OSS]`, `[Plugin.GSE]`).
- Unknown keys in `conf/zed.toml` fail startup.
- In TOML, durations are nanoseconds and sizes are bytes. Environment variables also accept durations like `10s` and sizes like `10GB`.
- The default log level is `debug`, and `/debug/pprof/` is served unless `GIN_MODE=release`. For production set `ZED_LOG_LEVEL=info` and `GIN_MODE=release`.
- TLS: set `ZED_SERVER_TLS_CERTIFICATE_FILE` and `ZED_SERVER_TLS_KEY_FILE`.

See [Environment Variables](environment-variables.md) for the full list.
