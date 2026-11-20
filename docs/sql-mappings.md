# JSON and MySQL SQL mappings

Mappings accept either the existing JSON `properties` object or a JSON object
containing a single `sql` string. SQL is parsed as a schema; it is never executed
and no MySQL connection is made. HTTP request and response bodies remain JSON.

## Configuration

The existing TOML loader reads `default.toml`, or the path in `ZED_CONFIG_FILE`.
SQL mappings are enabled by default:

```toml
EnableSQLMappings = true
```

Set this to `false` to reject SQL mapping requests. The non-empty environment
variable `ZED_ENABLE_SQL_MAPPINGS` overrides TOML. JSON mappings are always
available. This setting controls schema input, not a database connection or a
startup schema-import job.

## API

Create an index with `PUT /api/index`:

```json
{
  "name": "users",
  "storage_type": "disk",
  "mappings": {
    "sql": "CREATE TABLE users (id BIGINT, name VARCHAR(255), bio TEXT, active BOOLEAN, created_at DATETIME(6), metadata JSON)"
  }
}
```

The same `mappings` object works with `PUT /es/users` and inside a template's
`template.mappings` object. To add fields to an existing index, use
`PUT /api/users/_mapping` or `PUT /es/users/_mapping` with:

```json
{ "sql": "CREATE TABLE users (country CHAR(2))" }
```

The table name in SQL is descriptive only: the API's index name or template
patterns still determine the target. Existing field-update restrictions apply.
Responses and persisted mappings use normalized JSON, not the original DDL.

Existing JSON input is unchanged:

```json
{
  "properties": {
    "name": { "type": "keyword" },
    "bio": { "type": "text", "analyzer": "standard" }
  }
}
```

Do not combine `sql` with `properties` or other mapping options in the same
object. Use JSON when you need explicit analyzers, date formats, field options,
or nested property definitions.

## Type conversion

| MySQL type                                                   | ZedSearch mapping                                                    |
| ------------------------------------------------------------ | -------------------------------------------------------------------- |
| CHAR, VARCHAR, ENUM, SET                                     | keyword                                                              |
| TINYTEXT, TEXT, MEDIUMTEXT, LONGTEXT                         | text                                                                 |
| Integer types, DECIMAL/NUMERIC, FLOAT/DOUBLE/REAL, YEAR, BIT | numeric                                                              |
| BOOL, BOOLEAN                                                | bool                                                                 |
| DATE                                                         | date, Go layout `2006-01-02`                                         |
| DATETIME, TIMESTAMP                                          | date, Go layout `2006-01-02 15:04:05` (fractional seconds accepted)  |
| TIME                                                         | keyword (MySQL TIME can represent a duration)                        |
| JSON                                                         | dynamic child-field inference, like the existing JSON object mapping |
