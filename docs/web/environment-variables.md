# Configuration and environment variables

Settings are applied in order:

1. Built-in defaults ([`pkg/config/default.toml`](https://github.com/vcaesar/zedsearch/blob/main/pkg/config/default.toml))
1. The optional `conf/zed.toml` in the working directory
1. Non-empty environment variables

Every TOML key is also an environment variable in uppercase, e.g. `zed_server_port` → `ZED_SERVER_PORT`. Unknown keys in `conf/zed.toml` fail startup. Durations are in nanoseconds, sizes in bytes.

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

Only list the keys you change; grouped keys keep their table header (`[Shard]`, `[S3]`, `[Etcd]`, ...).

## Server

| Variable | Default | Required | Description |
| --- | --- | --- | --- |
| ZED_FIRST_ADMIN_USER | - | On first run | First admin user. Only used to create the admin on first start. |
| ZED_FIRST_ADMIN_PASSWORD | - | On first run | Password for the first admin user. Change it after logging in. |
| ZED_SERVER_ADDRESS | (all interfaces) | No | IP address to bind to |
| ZED_SERVER_PORT | 4080 | No | HTTP listen port |
| ZED_SERVER_TLS_CERTIFICATE_FILE | - | No | TLS certificate file, enables HTTPS together with the key file |
| ZED_SERVER_TLS_KEY_FILE | - | No | TLS private key file |
| ZED_SERVER_MODE | node | No | `node` or `cluster`; `cluster` forces etcd metadata storage |
| ZED_SERVER_BODY_LIMIT | 104857600 | No | Max request body in bytes (100 MB). Only `-tags fiber` builds enforce it. |
| ZED_NODE_ID | 1 | No | Node id used for document id generation |
| ZED_DATA_PATH | ./data | No | Data directory, must be writable |
| ZED_METADATA_STORAGE | bolt | No | Metadata store: `bolt`, `badger` or `etcd` |
| ZED_ICE_COMPRESSOR | zstd | No | Segment compression: `zstd`, `snappy` or `s2` |
| ZED_LOG_LEVEL | debug | No | Log level: `debug`, `info`, `warn`, `error` |
| ZED_SWAGGER_ENABLE | true | No | Serve the swagger UI at `/swagger` |
| ZED_PROMETHEUS_ENABLE | false | No | Expose prometheus metrics at `/metrics` |

## Indexing and search

| Variable | Default | Description |
| --- | --- | --- |
| ZED_BATCH_SIZE | 1024 | Internal batch size for bulk indexing |
| ZED_MAX_RESULTS | 10000 | Maximum number of hits a search can return |
| ZED_AGGREGATION_TERMS_SIZE | 1000 | Maximum buckets of a terms aggregation |
| ZED_MAX_DOCUMENT_SIZE | 1000000 | Maximum size of a single document in bytes |
| ZED_ENABLE_TEXT_KEYWORD_MAPPING | false | Add a `field.keyword` sub-field to every `text` field |
| ZED_ENABLE_SQL_MAPPINGS | true | Accept MySQL `CREATE TABLE` schemas in `mappings.sql` |
| ZED_WAL_SYNC_INTERVAL | 1000000000 (1s) | How often the WAL is flushed to the index |
| ZED_WAL_REDOLOG_NO_SYNC | false | Skip the fsync after each redo log write; faster, but a crash can duplicate up to `ZED_BATCH_SIZE` documents |
| ZED_OBJECT_CACHE_SIZE | 10737418240 (10 GB) | Local disk LRU cache for object storage indexes under `<data>/objcache`, 0 disables |

## Shards (`[Shard]`)

| Variable | Default | Description |
| --- | --- | --- |
| ZED_SHARD_NUM | 3 | Default number of shards per index |
| ZED_SHARD_MAX_SIZE | 1073741824 (1 GB) | Max size of one shard generation before a new one is started |
| ZED_SHARD_GOROUTINE_NUM | 3 | Parallel goroutines for shard reads and writes |

## Object storage (`[S3]`, `[GCS]`, `[OSS]`)

Used by indexes created with `storage_type` `s3`, `gcs` or `oss`.

| Variable | Default | Description |
| --- | --- | --- |
| ZED_S3_ENDPOINT | s3.amazonaws.com | S3 endpoint, any S3-compatible store such as MinIO works |
| ZED_S3_BUCKET / ZED_S3_REGION | - | Bucket and region |
| ZED_S3_ACCESS_KEY_ID / ZED_S3_SECRET_ACCESS_KEY | - | Credentials |
| ZED_S3_USE_SSL | true | Use HTTPS |
| ZED_GCS_ENDPOINT | storage.googleapis.com | GCS through the S3-interoperable API |
| ZED_GCS_BUCKET / ZED_GCS_ACCESS_KEY_ID / ZED_GCS_SECRET_ACCESS_KEY | - | Bucket and HMAC keys |
| ZED_OSS_ENDPOINT | - | Alibaba Cloud OSS endpoint, e.g. `oss-cn-hangzhou.aliyuncs.com` |
| ZED_OSS_BUCKET / ZED_OSS_REGION / ZED_OSS_ACCESS_KEY_ID / ZED_OSS_SECRET_ACCESS_KEY | - | Bucket, region and credentials |

## Etcd (`[Etcd]`)

| Variable | Default | Description |
| --- | --- | --- |
| ZED_ETCD_ENDPOINTS | - | Comma-separated etcd endpoints, used by etcd metadata storage and cluster mode |
| ZED_ETCD_PREFIX | /zed | Key prefix |
| ZED_ETCD_USERNAME / ZED_ETCD_PASSWORD | - | Etcd credentials |

## Cluster (`[Cluster]`)

See [pkg/cluster](https://github.com/vcaesar/zedsearch/blob/main/pkg/cluster/README.md).

| Variable | Default | Description |
| --- | --- | --- |
| ZED_CLUSTER_ENABLE | false | Enable multi-node mode (needs etcd) |
| ZED_CLUSTER_NAME | ZedCluster | Cluster name, same on every node |
| ZED_CLUSTER_NODE_ID | (hostname) | Node id |
| ZED_CLUSTER_ADVERTISE_ADDR | - | Address other nodes use to reach this one, e.g. `http://10.0.0.1:4080` |
| ZED_CLUSTER_PARTITIONS | 256 | Number of partitions indexes hash to |
| ZED_CLUSTER_HEARTBEAT_INTERVAL | 5000000000 (5s) | Heartbeat interval |
| ZED_CLUSTER_HEARTBEAT_TTL | 15000000000 (15s) | Membership lease TTL |
| ZED_CLUSTER_REPLICAS | 1 | Replicas per partition |
| ZED_CLUSTER_REPLICATION | document | `document`, `segment` or `none` (writable object storage indexes use the remote translog) |
| ZED_CLUSTER_REPLICATION_INTERVAL | 10000000000 (10s) | Segment checkpoints, remote flushes and catch-up |
| ZED_CLUSTER_SECRET | - | Shared secret for node-to-node replication, required when replication is on |

## Data tiers (`[Tier]`)

| Variable | Default | Description |
| --- | --- | --- |
| ZED_TIER_ENABLE | false | Run the lifecycle checker that moves indexes between hot, warm and cold |
| ZED_TIER_CHECK_INTERVAL | 600000000000 (10m) | How often lifecycle policies are evaluated |
| ZED_TIER_COLD_STORAGE_TYPE | s3 | Object store for warm/cold indexes: `s3`, `gcs` or `oss` |

## Plugins (`[Plugin.ES]`, `[Plugin.GSE]`)

| Variable | Default | Description |
| --- | --- | --- |
| ZED_PLUGIN_ES_VERSION | - | Elasticsearch version reported to clients; by default taken from the client's User-Agent |
| ZED_PLUGIN_GSE_ENABLE | false | Enable the GSE Chinese analyzer |
| ZED_PLUGIN_GSE_ENABLE_STOP | true | Load the GSE stop word dictionary |
| ZED_PLUGIN_GSE_ENABLE_HMM | true | Use HMM for search segmentation |
| ZED_PLUGIN_GSE_DICT_EMBED | small | Embedded dictionary: `small` or `big` |
| ZED_PLUGIN_GSE_DICT_PATH | ./plugins/gse/dict | Directory for user dictionaries |

## Telemetry, errors and profiling

All are off unless set.

| Variable | Default | Description |
| --- | --- | --- |
| ZED_TELEMETRY | false | Send anonymous usage telemetry |
| ZED_SENTRY | false | Send error reports to Sentry |
| ZED_SENTRY_DSN | - | Sentry DSN, required with `ZED_SENTRY` |
| ZED_PROFILER | false | Continuous profiling with Pyroscope |
| ZED_PROFILER_SERVER | - | Pyroscope server, required with `ZED_PROFILER` |
| ZED_PROFILER_API_KEY | - | Pyroscope API key |
| ZED_PROFILER_FRIENDLY_PROFILE_ID | (instance id) | Application suffix, `zedsearch-<id>` |
