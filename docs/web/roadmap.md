# Roadmap

The original [ZincSearch roadmap](https://zincsearch-docs.zinc.dev/roadmap/) is done in ZedSearch.

## Done

- [x] **High availability** — multi-node cluster with etcd coordination, fenced partition handoff and replicas (`document`, `segment` and `remote` replication). See [pkg/cluster](../../pkg/cluster/README.md).
- [x] **Distributed reads and writes** — every index is owned by one partition owner, requests are routed to it, and searches over several indexes scatter-gather across nodes (`/api/cluster/nodes`, `/api/cluster/partitions`).
- [x] **Geospatial search** — `geo_point` fields with `geo_bounding_box`, `geo_distance`, `geo_polygon` and `geo_shape` queries.

Also shipped since the fork:

- [x] Vector fields and `knn` search
- [x] Nested and join (`has_child`, `has_parent`, `parent_id`) queries
- [x] SQL search (`/api/_sql`, `/es/_sql`, `/es/_sql/translate`) and MySQL `CREATE TABLE` mappings ([sql-mappings](../sql-mappings.md))
- [x] Pipeline aggregations (`bucket_script`, `bucket_sort`, `derivative`, `moving_fn`, ...)
- [x] Data tiers (hot / warm / cold) and index lifecycle policies (`/api/index/{index}/_tier`, `/es/_ilm/policy`)
- [x] Object storage indexes on S3 / MinIO, GCS and Alibaba OSS with a local cache
- [x] Metadata storage on bolt, badger or etcd
- [x] Opt-in [logs indexes](../../pkg/logs/README.md)
- [x] Elasticsearch-compatible `_reindex`, `_count`, `_delete_by_query`, `_field_caps`, `_aliases`, `_cat` and `_cluster/health`
- [x] React web console embedded in the binary (`-tags web`), gin or fiber HTTP engine

## Next

Raise an [issue](https://github.com/vcaesar/zedsearch/issues) if you are looking for something.
