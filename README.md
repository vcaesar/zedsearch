<p align="center">
  <img src="./docs/logo_2/icon.svg" alt="ZedSearch" width="128" height="128">
</p>

[![CI](https://github.com/vcaesar/zedsearch/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/vcaesar/zedsearch/actions/workflows/ci.yml)
[![Nightly](https://github.com/vcaesar/zedsearch/actions/workflows/nightly.yml/badge.svg?branch=main)](https://github.com/vcaesar/zedsearch/actions/workflows/nightly.yml)
[![Release](https://github.com/vcaesar/zedsearch/actions/workflows/release.yml/badge.svg)](https://github.com/vcaesar/zedsearch/actions/workflows/release.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/vcaesar/zedsearch.svg)](https://pkg.go.dev/github.com/vcaesar/zedsearch)
[![Go Version](https://img.shields.io/github/go-mod/go-version/vcaesar/zedsearch)](./go.mod)
[![golangci-lint](https://img.shields.io/badge/linter-golangci--lint-blue)](https://golangci-lint.run/)
[![Release](https://img.shields.io/github/v/release/vcaesar/zedsearch)](https://github.com/vcaesar/zedsearch/releases/latest)
[![Docs](https://img.shields.io/badge/Docs-Docs-green)](./docs/web/index.md) [![codecov](https://codecov.io/github/vcaesar/zedsearch/branch/main/graph/badge.svg)](https://codecov.io/github/vcaesar/zedsearch)
<a href="https://discord.gg/535uZPakJw"><img src="https://img.shields.io/discord/1484658282777018551.svg?logo=discord&logoColor=white&label=Discord&color=5865F2" alt="Join the Discord chat at https://discord.gg/535uZPakJw"></a>

English | [简体中文](langs/README.zh.md) | [繁體中文](langs/README.zht.md) | [日本語](langs/README.ja.md) | [한국어](langs/README.ko.md) | [Français](langs/README.fr.md) | [Deutsch](langs/README.de.md) | [Español](langs/README.es.md) | [Русский](langs/README.ru.md) | [Português](langs/README.pt.md)

# ZedSearch

ZedSearch is a search engine that does full text indexing. It is a lightweight alternative to Elasticsearch and runs using a fraction of the resources. It uses [bluge](https://github.com/blugelabs/bluge) (via the [vcaesar/riot](https://github.com/vcaesar/riot) fork) as the underlying indexing library.

It is very simple and easy to operate as opposed to Elasticsearch which requires a couple dozen knobs to understand and tune. You can get ZedSearch up and running in 2 minutes.

It is a drop-in replacement for Elasticsearch if you are just ingesting data using APIs and searching using kibana (Kibana is not supported with ZedSearch. ZedSearch provides its own UI).

Check the below video for a quick demo of ZedSearch.

<!-- [![Zed Youtube](./screenshots/zed-youtube.jpg)](https://www.youtube.com/watch?v=aZXtuVjt1ow) -->

# Why ZedSearch

While Elasticsearch is a very good product, it is complex and requires lots of resources and is more than a decade old. I built ZedSearch so it becomes easier for folks to use full text search indexing without doing a lot of work.

# Features:

go + gin + react

1. Provides full text indexing capability
2. Single binary for installation and running. Binaries available under releases for multiple platforms.
3. Web UI for querying data written in React (embedded in the binary)
4. Compatibility with Elasticsearch APIs for ingestion of data (single record and bulk API)
5. Out of the box authentication
6. Schema less - No need to define schema upfront and different documents in the same index can have different fields.
7. Index storage in disk
8. aggregation support
9. Opt-in [logs indexes](./pkg/logs/README.md) with event-time normalization and log search defaults

# Benchmarks

Seven search-application queries over 50,000 documents, top 10 plus the exact total, on a MacBook Pro M1 with every server in Docker: a filtered term query, an OR over two fields, a sort by price over all documents, page 101, a terms and a histogram aggregation, and highlighting. Lower is better, except QPS; "—" means the engine cannot express the query (Meilisearch has no numeric histogram, OpenObserve does not highlight).

| Query | ZedSearch | Elasticsearch 9.5 | OpenSearch 3.9 | Meilisearch 1.56 | OpenObserve 1.1 |
|---|---:|---:|---:|---:|---:|
| `filtered` (1 client) | **0.89 ms** | 1.38 ms | 1.44 ms | 1.87 ms | 9.44 ms |
| `filtered` (8 clients) | 2,959 QPS | 2,742 QPS | **3,066 QPS** | 1,924 QPS | 321 QPS |
| `sort_price` (50k docs) | 1.85 ms | 2.52 ms | 1.58 ms | **1.09 ms** | 4.40 ms |
| `multi_field` title + body | **0.63 ms** | 1.44 ms | 1.51 ms | 1.19 ms | 21.0 ms |
| `deep_page` from 1000 | **1.24 ms** | 1.42 ms | 1.47 ms | 12.7 ms | 18.1 ms |
| `agg_terms` `category` | **1.48 ms** | 1.66 ms | 2.12 ms | 1.63 ms | 18.6 ms |
| `agg_histogram` `price` | **1.00 ms** | 1.67 ms | 1.72 ms | — | 18.7 ms |
| `highlight` `body` | **0.81 ms** | 1.96 ms | 1.97 ms | 1.92 ms | — |

### Single queries (v2)

The v2 queries from the same sessions, one query type each. Meilisearch cannot express OR or exact phrases.

| Query | ZedSearch | Elasticsearch 9.5 | OpenSearch 3.9 | Meilisearch 1.56 | OpenObserve 1.1 |
|---|---:|---:|---:|---:|---:|
| `term` rare | **0.50 ms** | 1.43 ms | 1.16 ms | 1.14 ms | 15.7 ms |
| `term` mid | **0.58 ms** | 1.28 ms | 1.27 ms | 1.16 ms | 17.6 ms |
| `term` keyword | **0.57 ms** | 1.75 ms | 2.32 ms | 1.03 ms | 5.59 ms |
| `bool` should | **0.63 ms** | 1.61 ms | 1.75 ms | — | 26.7 ms |
| `bool` must | **1.01 ms** | 2.36 ms | 3.30 ms | 1.28 ms | 24.1 ms |
| `range` | **0.64 ms** | 1.32 ms | 1.28 ms | 1.32 ms | 5.31 ms |
| `match_all` | **0.66 ms** | 1.48 ms | 1.38 ms | 1.03 ms | 4.68 ms |
| `prefix` `w1*` | **0.71 ms** | 1.87 ms | 2.66 ms | 1.32 ms | 11.9 ms |
| `match_phrase` | **10.3 ms** | 12.7 ms | 12.6 ms | — | 16.3 ms |
| `bool` should (8 clients) | **5,179 QPS** | 2,707 QPS | 2,616 QPS | — | 131 QPS |

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

You can use ZedSearch to index and search any data. Here are some examples that folks have created to index and search enron email dataset using zedsearch:

1. https://github.com/jorgeloaiza48/Enron-Email-DataSet
1. https://github.com/jhojanperlaza/email_search_engine
1. https://github.com/carlosarraes/zinmail
1. https://github.com/devjopa/golab-search
1. https://github.com/avaco2312/zincsearch
1. https://github.com/paolorossig/email-indexer
1. https://github.com/ulimonte05/zincsearching
