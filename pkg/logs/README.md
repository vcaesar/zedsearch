# Logs indexes

`pkg/logs` adds an opt-in logs profile to the existing ZedSearch engine, 
It reuses ZedSearch's WAL, shards, storage backends, mappings, search, authentication and lifecycle settings.

## Create

`POST /api/index`:

```json
{
  "name": "app-logs",
  "storage_type": "disk",
  "settings": { "index_type": "logs" }
}
```

Alternatively, `PUT /es/app-logs` with `{"settings":{"index_type":"logs"}}`.
The setting is persisted and returned by `GET /api/app-logs/_settings` and the
corresponding `/es` endpoint. Omitted/empty `index_type` means `standard`;
unknown values are rejected. The settings API cannot change the profile of an
existing index: create a new index to change profiles.

Default mappings:

| Fields | Type |
| --- | --- |
| `@timestamp` | Indexed, sortable, aggregatable date |
| `message`, `log` | Full-text |
| `level`, `log.level`, `service.name`, `host.name`, `trace_id`, `span_id`, `tags` | Keyword |
| `_timestamp` | Source-only input alias; use `@timestamp` for time queries |

Other fields are dynamically mapped as usual. Explicit mappings override these
presets, except that `@timestamp` must remain a date. Neither `message` nor `log`
is required, and neither is renamed or copied into the other. ECS-style objects
such as `{"log":{"level":"error"},"service":{"name":"api"}}` keep their
original structure in `_source` and are searchable through dotted field names.

## Ingest

Use existing authenticated endpoints; no additional permissions or routes are needed:

- `POST /api/app-logs/_doc` or `/es/app-logs/_doc`: one JSON object.
- `POST /api/app-logs/_multi`: one JSON object per line (NDJSON).
- `POST /api/app-logs/_bulkv2`: `{"records":[{...},{...}]}` (not a bare array).
- `POST /api/_bulk`, `/api/app-logs/_bulk`, `/es/_bulk` or `/es/app-logs/_bulk`:
  Elasticsearch action/document pairs. Include a trailing newline.

Example `/es/_bulk` payload:

```json
{"index":{"_index":"app-logs"}}
{"@timestamp":"2026-09-21T12:00:00.123456789Z","message":"request failed","level":"error","service":{"name":"api"}}
{"index":{"_index":"app-logs"}}
{"_timestamp":1790006400123456,"log":"request completed","level":"info"}
```

Event-time precedence is non-null `@timestamp`, then non-null `_timestamp`, then
UTC ingestion time. Strings default to RFC3339 (offsets and fractional seconds
are supported). 
Each field's own date mapping `format`/`time_zone` applies only
to that field: a custom `@timestamp` format does not affect `_timestamp`, and an
explicit `_timestamp` date mapping applies to the alias.
Numeric epochs follow ZedSearch's existing magnitude-based seconds/milliseconds/microseconds/nanoseconds detection.
Prefer RFC3339 strings for nanosecond precision in JSON, since JSON numbers are
decoded as float64. The logs WAL preserves the normalized instant without a
numeric round-trip.
