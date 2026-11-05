# Quickstart

1. Start ZedSearch.
1. Load a few documents and search them.

## Start ZedSearch

Use a release binary or Docker image (both include the web UI); see [Installation](installation.md) for other options.

### Binary

Download the archive for your platform from the [releases page](https://github.com/vcaesar/zedsearch/releases/latest), extract it, then:

    ZED_FIRST_ADMIN_USER=admin ZED_FIRST_ADMIN_PASSWORD=Complexpass#123 ./zedsearch

### Docker

    mkdir data
    docker run -d --name zedsearch -p 4080:4080 \
        -v "$PWD/data:/data" -e ZED_DATA_PATH=/data \
        -e ZED_FIRST_ADMIN_USER=admin -e ZED_FIRST_ADMIN_PASSWORD=Complexpass#123 \
        ghcr.io/vcaesar/zedsearch:latest

If the container cannot write to `./data`, run `sudo chown 10001:10001 ./data`.

### Source

    ZED_FIRST_ADMIN_USER=admin ZED_FIRST_ADMIN_PASSWORD=Complexpass#123 go run cmd/zedsearch/main.go

This is API-only. Use `go run cmd/zedsearch/main.go web` for the UI (Node.js 24+ and npm).

Check that it is up:

```shell
curl http://localhost:4080/healthz
curl http://localhost:4080/version
```

## Load data

Write a few documents with the bulk API (Elasticsearch NDJSON format, trailing newline required). The index is created on first write.

```shell
cat > books.ndjson <<'EOF'
{"index":{"_index":"books"}}
{"title":"A lightweight search engine","author":"Ada","year":2024}
{"index":{"_index":"books"}}
{"title":"Full text search in Go","author":"Linus","year":2025}
{"index":{"_index":"books"}}
{"title":"Cooking for engineers","author":"Grace","year":2023}
EOF

curl -u admin:Complexpass#123 http://localhost:4080/api/_bulk --data-binary "@books.ndjson"
```

A single document can be added with `POST /api/books/_doc`:

```shell
curl -u admin:Complexpass#123 http://localhost:4080/api/books/_doc \
  -H 'Content-Type: application/json' \
  -d '{"title":"Search at scale","author":"Ken","year":2026}'
```

Writes go through a WAL, so documents become searchable shortly after the request returns.

## Search

Native API (`POST /api/:index/_search`):

```shell
curl -u admin:Complexpass#123 http://localhost:4080/api/books/_search \
  -H 'Content-Type: application/json' \
  -d '{"search_type":"match","query":{"field":"title","term":"search"},"sort_fields":["-year"],"from":0,"max_results":10}'
```

`search_type` is one of `match`, `matchphrase`, `term`, `prefix`, `wildcard`, `fuzzy`, `querystring`, `daterange`, `matchall`, `alldocuments`.

Elasticsearch-compatible Query DSL (`POST /es/:index/_search`):

```shell
curl -u admin:Complexpass#123 http://localhost:4080/es/books/_search \
  -H 'Content-Type: application/json' \
  -d '{"query":{"match":{"title":"search"}},"aggs":{"by_year":{"terms":{"field":"year"}}},"size":10}'
```

Count documents:

```shell
curl -u admin:Complexpass#123 http://localhost:4080/es/books/_count
```

With the web UI, open [http://localhost:4080](http://localhost:4080), log in, select the `books` index and search for `search`.

See the [API documentation](https://github.com/zedsearch/zedsearch-docs) for the full list of endpoints.
