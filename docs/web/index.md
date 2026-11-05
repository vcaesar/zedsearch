# Welcome to ZedSearch

ZedSearch is a full text search engine and a lightweight alternative to Elasticsearch. It runs as a single Go binary, uses a fraction of the resources and indexes with [bluge](https://github.com/blugelabs/bluge) (via the [vcaesar/riot](https://github.com/vcaesar/riot) fork). It is a fork of ZincSearch.

Source code: [github.com/vcaesar/zedsearch](https://github.com/vcaesar/zedsearch). Community: [Discord](https://discord.gg/535uZPakJw).

## Why ZedSearch

Elasticsearch is a very good product, but it is complex, needs lots of resources and dozens of settings to tune. ZedSearch makes full text indexing easy to run: start one binary, push JSON, search.

## Features

1. Full text indexing, schema less: no mapping is needed upfront and documents in the same index can have different fields.
1. Single binary for multiple platforms, see [releases](https://github.com/vcaesar/zedsearch/releases).
1. Native API under `/api` and an Elasticsearch-compatible API under `/es` for ingestion (single document and `_bulk`), Query DSL search, `_msearch`, `_count`, mappings, settings, aliases, index templates and ILM policies.
1. SQL search (`/api/_sql`, `/es/_sql`).
1. Aggregations and highlighting.
1. Built-in authentication with users and roles.
1. Index storage on local disk or object storage (S3/MinIO, Google Cloud Storage, Alibaba Cloud OSS), with hot/warm/cold data tiers.
1. Metadata in bolt (default), badger or etcd.
1. Optional multi-node cluster coordinated through etcd, with replication.
1. Opt-in [logs indexes](https://github.com/vcaesar/zedsearch/blob/main/pkg/logs/README.md) with event-time normalization.
1. Prometheus metrics on `/metrics`.
1. React web UI. Release binaries and Docker images embed it; plain `go build` produces an API-only binary (see [Installation](installation.md)).

## Docs

- [Quickstart](quickstart.md)
- [Installation](installation.md)
- [Configuration and environment variables](environment-variables.md)
- [Roadmap](roadmap.md)
- API: [zedsearch-docs](https://github.com/zedsearch/zedsearch-docs)
