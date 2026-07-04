# HTTP API

The API is served on `--host` (default `:8080`). Health, readiness and metrics
are served by a **separate ops server** on `--ops-host` (default `:9090`) and are
**not** part of the OpenAPI spec.

For an interactive, always-in-sync view of the API, see the
[API Explorer](api-explorer.md) — it renders the OpenAPI schema directly.

## API endpoints (`--host`, `:8080`)

| Method | Path                          | Auth   | Purpose                                               |
| ------ | ----------------------------- | ------ | ----------------------------------------------------- |
| `POST` | `/v1/vlbackup/snapshot`       | —      | Snapshot a partition and upload it to object storage. |
| `POST` | `/v1/vlbackup/transfer`       | —      | Transfer sealed partitions to a peer vlbackup.        |
| `POST` | `/v1/vlbackup/transfer/receive` | Bearer | Target side: receive a partition stream.            |
| `POST` | `/v1/vlbackup/transfer/attach`  | Bearer | Target side: attach a received partition.           |
| `POST` | `/v1/vlbackup/restore`        | —      | Restore a partition from object storage.              |

The `Bearer` endpoints require `Authorization: Bearer <token>` when `VLBACKUP_TRANSFER_AUTH_KEY` is set (see [Configuration](../user-guide/configuration.md)).

## Ops endpoints (`--ops-host`, `:9090`)

| Endpoint       | Description                                              |
| -------------- | -------------------------------------------------------- |
| `GET /healthz` | Liveness. Returns `200` when the process is up.          |
| `GET /readyz`  | Readiness. Returns `200` when ready to serve.            |
| `GET /metrics` | Prometheus exposition format. See [Metrics](metrics.md). |

## `POST /v1/vlbackup/snapshot`

Snapshots the given partition and uploads it to `destination_url`.

### Request body

```json
{
  "destination_url": "gs://my-bucket/vlbackup/",
  "partition_prefix": "20260703"
}
```

| Field              | Required | Description                                                                                                 |
| ------------------ | -------- | ----------------------------------------------------------------------------------------------------------- |
| `destination_url`  | yes      | Backend + bucket + prefix, e.g. `gs://bucket/prefix/` or `s3://bucket/prefix/`. Scheme selects the backend. |
| `partition_prefix` | no       | Partition to snapshot (`YYYYMMDD`). Defaults to **yesterday** UTC.                                          |

### Example

```sh
curl -sL -XPOST http://vlbackup:8080/v1/vlbackup/snapshot \
  -H "Content-Type: application/json" \
  -d '{"destination_url": "s3://my-bucket/backups/", "partition_prefix": "20260703"}'
```

### Responses

| Status                      | Meaning                                                                                     |
| --------------------------- | ------------------------------------------------------------------------------------------- |
| `202 Accepted`              | Snapshot created and uploaded, or the partition held no data (empty acknowledgement).       |
| `400 Bad Request`           | Malformed body, unparseable `destination_url`, or an unsupported scheme.                    |
| `500 Internal Server Error` | VictoriaLogs, storage, or upload failure. Body is `{"error": "...", "code": 500}`.          |

Each returned snapshot path is uploaded as `<prefix>/<partition>.tar.gz`, then the snapshot is deleted from VictoriaLogs.

## `POST /v1/vlbackup/transfer`

Transfers per-day partitions from this ("source") VictoriaLogs to another ("target") instance running its own vlbackup sidecar. See [Partition Transfer](../user-guide/partition-transfer.md) for the full mechanism.

### Request body

```json
{
  "target_url": "http://vlbackup-target:8080",
  "range": {
    "from": "now-7d/d",
    "to": "now/d"
  }
}
```

| Field        | Required | Description                              |
| ------------ | -------- | ---------------------------------------- |
| `target_url` | yes      | Base URL of the target vlbackup sidecar. |
| `range.from` | yes      | Start of the range (time expression).    |
| `range.to`   | no       | End of the range. Defaults to now.       |

Each bound is a **time expression** `<anchor>[+-<duration>][/<rounding>]`:

- `anchor` (required): `now` or an RFC3339 date.
- `duration` (optional): `<int><unit>`, e.g. `-7d`, `+12h`.
- `rounding` (optional): `/<unit>`, truncates the result **down** to that unit.

Units: `y` (year), `M` (month), `w` (week, starts Monday), `d` (day), `h` (hour), `m` (minute), `s` (second). All math is in UTC. Examples: `now-7d/d` (start of 7 days ago), `now/d` (start of today), `2026-07-01T00:00:00Z`, `2026-07-01T00:00:00Z-2d/d`.

The range is interpreted as UTC days `[from, to)`. **Today's active partition is never transferred** — only sealed days strictly before today UTC are eligible.

### Example

```sh
curl -sL -XPOST http://vlbackup-source:8080/v1/vlbackup/transfer \
  -H "Content-Type: application/json" \
  -d '{"target_url": "http://vlbackup-target:8080", "range": {"from": "now-7d/d"}}'
```

### Response

```json
{"transferred": ["20260701"], "skipped": ["20260702"], "errors": []}
```

- `transferred` — days successfully moved to the target.
- `skipped` — days already present on the target (source data left untouched).
- `errors` — per-day failures.

Status is `200` when there are no hard errors, `500` otherwise. Both carry the same `TransferResponse` summary; `400` (malformed body / invalid range or target URL) returns an `ErrorResponse` instead.

## `POST /v1/vlbackup/transfer/receive` and `/attach`

These are the **target-side** endpoints, called by the source vlbackup — not by operators. They are protected by the shared bearer token when `VLBACKUP_TRANSFER_AUTH_KEY` is set.

- `/v1/vlbackup/transfer/receive?partition=YYYYMMDD` — accepts an `application/gzip` `tar.gz` stream and extracts it under `<data-path>/partitions/`. Returns `{"partition": "...", "bytes_written": N}`. `409` if the partition already exists.
- `/v1/vlbackup/transfer/attach?partition=YYYYMMDD` — attaches the received partition in VictoriaLogs. Returns `{"partition": "..."}`.

Both return `401` when the bearer token is missing or wrong.

If the source dies between detaching and attaching, the data is present but unattached on the target; recover manually:

```sh
curl -XPOST -H "Authorization: Bearer $TOKEN" \
  "http://vlbackup-target:8080/v1/vlbackup/transfer/attach?partition=20260702"
```

## `POST /v1/vlbackup/restore`

Downloads a partition snapshot (a `tar.gz`) from `source_url`, extracts it into the local data path and attaches it to VictoriaLogs.

### Request body

```json
{
  "source_url": "gs://my-bucket/vlbackup/",
  "partition_prefix": "20260703"
}
```

| Field              | Required | Description                                                                    |
| ------------------ | -------- | ------------------------------------------------------------------------------ |
| `source_url`       | yes      | Object Storage source URL, e.g. `gs://bucket/prefix/` or `s3://bucket/prefix/`. |
| `partition_prefix` | yes      | Partition to restore (`YYYYMMDD`).                                             |

### Example

```sh
curl -sL -XPOST http://vlbackup:8080/v1/vlbackup/restore \
  -H "Content-Type: application/json" \
  -d '{"source_url": "gs://my-bucket/vlbackup/", "partition_prefix": "20260703"}'
```

### Responses

| Status                      | Meaning                                                              |
| --------------------------- | ------------------------------------------------------------------- |
| `202 Accepted`              | Snapshot restored and attached; body `{"partition", "bytes_written"}`. |
| `400 Bad Request`           | Malformed body or unsupported `source_url` scheme.                  |
| `404 Not Found`             | The requested snapshot was not found in object storage.             |
| `409 Conflict`              | The partition is already attached locally.                          |
| `500 Internal Server Error` | Download or restore failure.                                        |
