<p align="center">
  <img src="./docs/logo_2/icon.svg" alt="ZedSearch" width="128" height="128">
</p>

[![CI](https://github.com/vcaesar/zedsearch/actions/workflows/ci.yml/badge.svg?branch=dev)](https://github.com/vcaesar/zedsearch/actions/workflows/ci.yml)
[![Nightly](https://github.com/vcaesar/zedsearch/actions/workflows/nightly.yml/badge.svg?branch=dev)](https://github.com/vcaesar/zedsearch/actions/workflows/nightly.yml)
[![Release](https://github.com/vcaesar/zedsearch/actions/workflows/release.yml/badge.svg)](https://github.com/vcaesar/zedsearch/actions/workflows/release.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/vcaesar/zedsearch.svg)](https://pkg.go.dev/github.com/vcaesar/zedsearch)
[![Go Version](https://img.shields.io/github/go-mod/go-version/vcaesar/zedsearch)](./go.mod)
[![golangci-lint](https://img.shields.io/badge/linter-golangci--lint-blue)](https://golangci-lint.run/)
[![Release](https://img.shields.io/github/v/release/vcaesar/zedsearch)](https://github.com/vcaesar/zedsearch/releases/latest)
[![Docs](https://img.shields.io/badge/Docs-Docs-green)](./docs/web/index.md) [![codecov](https://codecov.io/gh/vcaesar/zedsearch/graph/badge.svg)](https://app.codecov.io/gh/vcaesar/zedsearch)
<a href="https://discord.gg/535uZPakJw"><img src="https://img.shields.io/discord/1484658282777018551.svg?logo=discord&logoColor=white&label=Discord&color=5865F2" alt="Join the Discord chat at https://discord.gg/535uZPakJw"></a>

English | [简体中文](langs/README.zh.md) | [繁體中文](langs/README.zht.md) | [日本語](langs/README.ja.md) | [한국어](langs/README.ko.md) | [Français](langs/README.fr.md) | [Deutsch](langs/README.de.md) | [Español](langs/README.es.md) | [Русский](langs/README.ru.md) | [Português](langs/README.pt.md)

# ZedSearch

ZedSearch is a search engine that does full text and vector indexing, and cloud-native platform for observability logs, metrics, traces, analytics, monitoring, AI.
It is a lightweight alternative to Elasticsearch, OpenSearch, Datadog, Splunk and runs using a fraction of the resources.
It uses [vcaesar/riot](https://github.com/vcaesar/riot) as the underlying indexing library.

It is very simple and easy to operate as opposed to Elasticsearch which requires a couple dozen knobs to understand and tune. You can get ZedSearch up and running in a minute.

It is a drop-in replacement for Elasticsearch if you only ingest and search data through its APIs. Kibana is not supported, ZedSearch provides its own UI.

<!-- Check the below video for a quick demo of ZedSearch. -->
<!-- [![Zed Youtube](./screenshots/zed-youtube.jpg)](https://www.youtube.com/watch?v=aZXtuVjt1ow) -->

## Homebrew

macOS and Linux, via [vcaesar/homebrew-tap](https://github.com/vcaesar/homebrew-tap):

```sh
brew install vcaesar/tap/zedsearch
```

## Docker

Images are on [Docker Hub](https://hub.docker.com/r/riotgo/zedsearch) as `riotgo/zedsearch` (linux/amd64, linux/arm64, linux/arm/v7):

Set `ZED_FIRST_ADMIN_PASSWORD` in your environment to your chosen password before running:

```sh
docker run -d --name zedsearch \
    -p 4080:4080 \
    -v zedsearch-data:/data -e ZED_DATA_PATH=/data \
    -e ZED_FIRST_ADMIN_USER="admin" \
    -e ZED_FIRST_ADMIN_PASSWORD="your_password_zed#123" \
    riotgo/zedsearch:latest
```

The named volume `zedsearch-data` is writable by the container user (uid 10001); to use a host directory instead, run `mkdir -p ./data && sudo chown 10001:10001 ./data` and mount `-v "$PWD/data:/data"`.

## Cluster

Nodes share an etcd and own the indexes between them, with replicas and failover; any node serves any request. Every node needs a unique `ZED_CLUSTER_NODE_ID`, a `ZED_CLUSTER_ADVERTISE_ADDR` the other nodes can reach and the same `ZED_CLUSTER_SECRET`. See [pkg/cluster](./pkg/cluster/README.md) for the model, settings and limits.

### Docker

```sh
# Use your own admin password and a fresh cluster secret for every deployment.
ZED_PASSWORD='your_password_zed#123'
ZED_SECRET="$(openssl rand -hex 32)"

docker network create zed
docker run -d --name zed-etcd --network zed --restart unless-stopped \
    -v zed-etcd-data:/etcd-data quay.io/coreos/etcd:v3.7.3 etcd \
    --listen-client-urls http://0.0.0.0:2379 --advertise-client-urls http://zed-etcd:2379 \
    --data-dir /etcd-data

# Node to node traffic, with the secret, is plain HTTP on the private network "zed",
# so ports are published on localhost only. Across hosts use an encrypted network, or
# advertise https:// addresses and serve them: ZED_SERVER_TLS_CERTIFICATE_FILE and
# ZED_SERVER_TLS_KEY_FILE on every node (or a TLS proxy in front of it), with a
# certificate the other nodes trust (system roots, or a private CA added with
# SSL_CERT_FILE / SSL_CERT_DIR as k8s/cluster.yaml does).
# --restart retries a node that started before etcd.
for i in 1 2 3; do
  docker run -d --name zed$i --network zed -p 127.0.0.1:408$i:4080 --restart unless-stopped \
    -v zed$i-data:/data -e ZED_DATA_PATH=/data \
    -e ZED_FIRST_ADMIN_USER="admin" -e ZED_FIRST_ADMIN_PASSWORD="$ZED_PASSWORD" \
    -e ZED_CLUSTER_ENABLE=true -e ZED_CLUSTER_NAME=prod -e ZED_CLUSTER_NODE_ID=node-$i \
    -e ZED_CLUSTER_ADVERTISE_ADDR=http://zed$i:4080 -e ZED_CLUSTER_SECRET="$ZED_SECRET" \
    -e ZED_ETCD_ENDPOINTS=http://zed-etcd:2379 \
    riotgo/zedsearch:latest
done

curl --retry 10 --retry-connrefused -u admin:"$ZED_PASSWORD" http://localhost:4081/api/cluster/nodes

# Back up the shared metadata in zed-etcd-data; the copy runs only after a successful save.
docker exec zed-etcd etcdctl snapshot save /etcd-data/snapshot.db &&
  docker cp zed-etcd:/etcd-data/snapshot.db ./etcd-snapshot.db

# Restore with every node stopped: `etcdutl snapshot restore` into a new volume, then
# empty the in-sync sets before the nodes start, since a replica listed in an old
# snapshot may have missed later writes. Steps: pkg/cluster/README.md, "Backup and restore".
```

### Kubernetes

[k8s/cluster.yaml](./k8s/cluster.yaml) runs a three-node StatefulSet `zedsearch-cluster` behind the Service `zedsearch`, with a single-node etcd (run three members for production):

```sh
# needs cert-manager for the node TLS certificates; nodes serve HTTPS
kubectl create namespace zedsearch
kubectl -n zedsearch create secret generic zedsearch-admin \
    --from-literal=ZED_FIRST_ADMIN_USER=admin --from-literal=ZED_FIRST_ADMIN_PASSWORD='your_password_zed#123'
kubectl -n zedsearch create secret generic zedsearch-cluster \
    --from-literal=ZED_CLUSTER_SECRET="$(openssl rand -hex 32)"
kubectl apply -f k8s/cluster.yaml
kubectl -n zedsearch wait --for=jsonpath='{.status.readyReplicas}'=3 statefulset/zedsearch-cluster --timeout=10m
kubectl -n zedsearch port-forward svc/zedsearch 4080:4080 &
kubectl -n zedsearch get secret zedsearch-tls -o jsonpath='{.data.ca\.crt}' | base64 -d > zedsearch-ca.crt

curl --retry 10 --retry-connrefused --cacert zedsearch-ca.crt -u admin:'your_password_zed#123' https://localhost:4080/api/cluster/nodes
```

# Why ZedSearch

Elasticsearch and similar engines are very good products, but they are complex, need a lot of memory and other resources, and carry more than a decade of design history.
I built ZedSearch so that full text and vector search, observability and data analytics become easier, faster and free to use without a lot of work.

# Features

go + gin (or fiber v3 with `-tags fiber`) + react

1. Fast queries and smaller index files
1. Provides full text and vector indexing capability
1. Single binary for installation and running. Binaries available under releases for multiple platforms.
1. Web UI for querying data written in React (embedded in the binary)
1. Compatibility with Elasticsearch APIs for ingestion of data (single record and bulk API)
1. Out of the box authentication
1. Schema less - No need to define schema upfront and different documents in the same index can have different fields.
1. Index storage in disk, s3, minio and others
1. Aggregations and broad Elasticsearch API compatibility and full features support
1. Multi-language analysis, including CJK
1. Opt-in [logs indexes](./pkg/logs/README.md) with event-time normalization and log search defaults, ingesting JSON, Splunk HEC, Loki and OTLP/HTTP JSON (OpenObserve-compatible) and Logstash events
1. kNN vector search and geo queries (`geo_distance`, `geo_bounding_box`, `geo_polygon`, `geo_shape`)
1. Hybrid search fusing full text and kNN hits: Elasticsearch `rrf` and `linear` retrievers (weights, `minmax`/`l2_norm`), legacy `rank.rrf`, and the OpenSearch `hybrid` query with an inline `search_pipeline` (`normalization-processor`, `score-ranker-processor` RRF)
1. SQL queries through the Elasticsearch SQL API (`/es/_sql`, `/api/_sql`, `/es/_sql/translate`): `SELECT` with `WHERE` (`LIKE`, `IN`, `BETWEEN`, `IS NULL`, `MATCH()`, `QUERY()`), `COUNT`/`SUM`/`AVG`/`MIN`/`MAX`, `GROUP BY` (`HISTOGRAM`, `DATE_HISTOGRAM`), `HAVING`, `ORDER BY`, `LIMIT`, with JSON, txt, csv or tsv output; and MySQL `CREATE TABLE` [schemas as mappings](./docs/sql-mappings.md)
1. High-availability [cluster](./pkg/cluster/README.md): etcd-coordinated partition ownership, fenced handoff, replicas and scatter-gather search
1. OpenTelemetry tracing over OTLP/HTTP (`zed_tracing_enable`); every request gets an `X-Trace-Id` that follows it across nodes
1. Fleet and `zedagent` (`agent/`): enroll agents, manage policies, ship log files and system metrics, or run the agent standalone
1. Robots-aware [web crawler](./pkg/crawler/README.md) (`cmd/zedcrawl`) with PageRank and bulk indexing into ZedSearch
1. Data tiers: hot (local disk), warm (object storage) and cold (read-only object storage shared by every node), switched with `/api/index/{index}/_tier` or by index lifecycle (`warm_after`, `cold_after`, `delete_after`) and ILM policies (`/es/_ilm/policy`)
1. Synonyms sets (`/es/_synonyms`) with search analyzer reload, and query rules (`/es/_query_rules`) that pin or exclude documents
1. [Learning to rank](./docs/learning-to-rank.md) rescoring with linear, tree and XGBoost models (`/es/_ml/trained_models`), evaluated in pure Go
1. More Elasticsearch APIs: `_msearch`, `_count`, `_delete_by_query`, `_reindex`, `_field_caps`, aliases, index templates, `_cat/indices`, `_cat/health` and `_cluster/health`

# Benchmarks

Seven search-application queries over 50,000 documents, top 10 plus the exact total, on a MacBook Pro M1 with every server in Docker: a filtered term query, an OR over two fields, a sort by price over all documents, page 101, a terms and a histogram aggregation, and highlighting. Lower is better, except QPS; "—" means the engine cannot express the query (Meilisearch has no numeric histogram, OpenObserve does not highlight).

| Query                      |     ZedSearch | ZincSearch 0.4.10 | Elasticsearch 9.5 | OpenSearch 3.9 | Meilisearch 1.56 | OpenObserve 1.1 |
| -------------------------- | ------------: | ----------------: | ----------------: | -------------: | ---------------: | --------------: |
| `filtered` (1 client)      |   **0.89 ms** |           3.43 ms |           1.38 ms |        1.44 ms |          1.87 ms |         9.44 ms |
| `filtered` (8 clients)     | **3,216 QPS** |           471 QPS |         2,742 QPS |      3,066 QPS |        1,924 QPS |         321 QPS |
| `sort_price` (50k docs)    |   **0.74 ms** |           76.0 ms |           2.52 ms |        1.58 ms |          1.09 ms |         4.40 ms |
| `multi_field` title + body |   **0.63 ms** |           2.04 ms |           1.44 ms |        1.51 ms |          1.19 ms |         21.0 ms |
| `deep_page` from 1000      |   **1.24 ms** |           3.40 ms |           1.42 ms |        1.47 ms |          12.7 ms |         18.1 ms |
| `agg_terms` `category`     |   **1.48 ms** |           5.55 ms |           1.66 ms |        2.12 ms |          1.63 ms |         18.6 ms |
| `agg_histogram` `price`    |   **1.00 ms** |           20.1 ms |           1.67 ms |        1.72 ms |                — |         18.7 ms |
| `highlight` `body`         |   **0.81 ms** |           2.27 ms |           1.96 ms |        1.97 ms |          1.92 ms |               — |

### Single queries (v2)

The v2 queries from the same sessions, one query type each. Meilisearch cannot express OR or exact phrases.

| Query                     |     ZedSearch | ZincSearch 0.4.10 | Elasticsearch 9.5 | OpenSearch 3.9 | Meilisearch 1.56 | OpenObserve 1.1 |
| ------------------------- | ------------: | ----------------: | ----------------: | -------------: | ---------------: | --------------: |
| `term` rare               |   **0.50 ms** |           1.03 ms |           1.43 ms |        1.16 ms |          1.14 ms |         15.7 ms |
| `term` mid                |   **0.58 ms** |           1.82 ms |           1.28 ms |        1.27 ms |          1.16 ms |         17.6 ms |
| `term` keyword            |   **0.57 ms** |           0.82 ms |           1.75 ms |        2.32 ms |          1.03 ms |         5.59 ms |
| `bool` should             |   **0.63 ms** |           1.12 ms |           1.61 ms |        1.75 ms |                — |         26.7 ms |
| `bool` must               |   **1.01 ms** |           1.79 ms |           2.36 ms |        3.30 ms |          1.28 ms |         24.1 ms |
| `range`                   |   **0.64 ms** |           2.56 ms |           1.32 ms |        1.28 ms |          1.32 ms |         5.31 ms |
| `match_all`               |   **0.66 ms** |           4.12 ms |           1.48 ms |        1.38 ms |          1.03 ms |         4.68 ms |
| `prefix` `w1*`            |   **0.71 ms** |          214.5 ms |           1.87 ms |        2.66 ms |          1.32 ms |         11.9 ms |
| `match_phrase`            |   **10.3 ms** |           55.6 ms |           12.7 ms |        12.6 ms |                — |         16.3 ms |
| `bool` should (8 clients) | **5,179 QPS** |         1,292 QPS |         2,707 QPS |      2,616 QPS |                — |         131 QPS |

### Footprint

Docker image (uncompressed), text index of the same 50,000 documents (about 25 MB for ZedSearch, Elasticsearch and OpenSearch after `_flush`), and memory / CPU over the whole run (100% = 1 vCPU), the data is the median of three runs.

| Resource             |   ZedSearch | ZincSearch 0.4.10 | Elasticsearch 9.5 | OpenSearch 3.9 | Meilisearch 1.56 |       OpenObserve 1.1 |
| -------------------- | ----------: | ----------------: | ----------------: | -------------: | ---------------: | --------------------: |
| Docker image         | **56.4 MB** |           72.4 MB |          1,195 MB |       1,311 MB |           370 MB |                434 MB |
| Text index, 50k docs |     25.9 MB |          133.3 MB |           25.8 MB |        25.7 MB |           454 MB | 14.2 MB (Not indexed) |
| Memory idle          |  **14 MiB** |            64 MiB |         2,674 MiB |      2,699 MiB |          179 MiB |               387 MiB |
| Memory peak          | **388 MiB** |           858 MiB |         3,185 MiB |      3,191 MiB |        1,014 MiB |               919 MiB |
| CPU mean             |    **134%** |              366% |              157% |           156% |             230% |                  140% |
| CPU peak             |        553% |              710% |              620% |           529% |             545% |              **519%** |

### Vector search

20,000 × 128-d cosine vectors, k=10, HNSW (M=16, ef_construction 100, 100 candidates), over HTTP on the same machine and corpus. Higher is better for recall, QPS and upload; the vector index is the data growth after the upload, before `_flush`.

| Metric                    |        ZedSearch | Elasticsearch 9.5 | OpenSearch 3.9 | Meilisearch 1.56 | Qdrant 1.20 | Milvus 3.0 | Weaviate 1.40 |
| ------------------------- | ---------------: | ----------------: | -------------: | ---------------: | ----------: | ---------: | ------------: |
| Recall@10                 |            0.939 |         **0.956** |          0.823 |            0.485 |       0.891 |      0.685 |         0.595 |
| Latency, 1 client         |      **1.47 ms** |           2.56 ms |        2.09 ms |          4.71 ms |     1.84 ms |    2.70 ms |       2.28 ms |
| QPS, 8 clients            |    **1,923 QPS** |           983 QPS |      1,385 QPS |        1,025 QPS |   1,653 QPS |  1,827 QPS |     1,706 QPS |
| Upload + index            | **10,070 vec/s** |       3,004 vec/s |    1,770 vec/s |        377 vec/s | 4,609 vec/s |  999 vec/s |   3,103 vec/s |
| Vector index, 20k × 128-d |          28.3 MB |           63.9 MB |        75.6 MB |      **25.8 MB** |     41.4 MB |    34.3 MB |       75.6 MB |

### Vector search, 3-node cluster

Medians of three runs: three nodes with 1 replica on one 8-vCPU Docker VM, ZedSearch with etcd and `document` replication. The nodes share the CPUs, so this measures replication overhead, not scale-out.

| Metric                                 |     ZedSearch ×3 |     OpenSearch ×3 |
| -------------------------------------- | ---------------: | ----------------: |
| Recall@10                              |        **0.960** |             0.905 |
| Latency, 1 client                      |      **1.81 ms** |           3.59 ms |
| QPS, 8 clients                         |    **1,611 QPS** |           970 QPS |
| Upload + index                         |  **4,157 vec/s** |       1,451 vec/s |
| Vector index after `_flush`, all nodes |      **56.5 MB** |           70.4 MB |
| Memory idle / peak, all nodes          | **78 / 902 MiB** | 4,517 / 5,360 MiB |

# Documentation

Documentation is available at [docs/web](./docs/web/index.md), API documentation at [zedsearch-docs](https://github.com/zedsearch/zedsearch-docs)

# Screenshots

## Search screen

![Search screen](./docs/hots/search-26.png)

## Index screen

![Index screen](./docs/hots/index-26.png)

# Getting started

## Quickstart

Check [Quickstart](./docs/web/quickstart.md)

# Releases

ZedSearch has hundreds of production installations.

Binaries for every platform are on the [releases page](https://github.com/vcaesar/zedsearch/releases/latest). The default archives are not UPX-compressed; for a smaller download, all Linux builds and the Windows amd64 build also ship a [UPX](https://upx.github.io/)-packed `*_upx.tar.gz` (slightly slower startup, and some antivirus tools may flag packed `.exe` files).

## Build

Set `ZED_FIRST_ADMIN_USER` / `ZED_FIRST_ADMIN_PASSWORD` or configure `conf/zed.toml` (see below), then run from the repository root:

```sh
go run cmd/zedsearch/main.go      # API-only, :4080
go run cmd/zedsearch/main.go web  # with the web UI (Node.js 24+ and npm)
```

`web` produces `test/zedsearch-web`, a standalone binary with the UI embedded.

### Configuration

Settings are applied in order: built-in defaults ([`pkg/config/default.toml`](./pkg/config/default.toml)), then the optional `conf/zed.toml` in the working directory, then non-empty environment variables. Every key is also an environment variable in uppercase, e.g. `zed_server_port` → `ZED_SERVER_PORT`.

```toml
# conf/zed.toml
zed_first_admin_user = "admin"
zed_first_admin_password = "change-me"
zed_server_port = "4080"
zed_data_path = "./data"
zed_log_level = "info"

[Shard]
zed_shard_num = 3
```

- Only list the keys you change; grouped keys keep their table header from `default.toml` (`[Shard]`, `[S3]`, `[Etcd]`, ...).
- Unknown keys fail startup. Durations are in nanoseconds, sizes in bytes.
- The first admin credentials only create the admin user on first start; change the password after logging in and never commit real credentials.

# Community

- How to develop and contribute to ZedSearch

  Check the [contributing guide](./CONTRIBUTING.md). Also check the [roadmap](./docs/web/roadmap.md)

# Examples

You can use ZedSearch to index and search any data. [examples/](./examples) has Python scripts that index a document, load Word files and search them.

Community projects that index and search the Enron email dataset with ZincSearch (ZedSearch keeps its `/api` endpoints):

1. https://github.com/jorgeloaiza48/Enron-Email-DataSet
1. https://github.com/jhojanperlaza/email_search_engine
1. https://github.com/carlosarraes/zinmail
1. https://github.com/devjopa/golab-search
1. https://github.com/avaco2312/zincsearch
1. https://github.com/paolorossig/email-indexer
1. https://github.com/ulimonte05/zincsearching
