# HTTP API

All endpoints are served by the same process on `--host` (default `:8080`).

| Method | Path | Auth | Purpose |
| --- | --- | --- | --- |
| `POST` | `/snapshot` | — | Snapshot a partition and upload it to object storage. |
| `POST` | `/api/v1/transfer` | — | Transfer sealed partitions to a peer vlbackup. |
| `POST` | `/api/v1/transfer/receive` | Bearer | Target side: receive a partition stream. |
| `POST` | `/api/v1/transfer/attach` | Bearer | Target side: attach a received partition. |
| `GET` | `/healthz` | — | Liveness probe. |
| `GET` | `/readyz` | — | Readiness probe. |
| `GET` | `/metrics` | — | Prometheus metrics. |

The `Bearer` endpoints require `Authorization: Bearer <token>` when `TRANSFERAUTHKEY` is set (see [Configuration](../user-guide/configuration.md)).

## `POST /snapshot`

Snapshots the given partition and uploads it to `destination_url`.

**Request body**

```json
{
  "destination_url": "gs://my-bucket/vlbackup/",
  "partition_prefix": "20260703"
}
```

| Field | Required | Description |
| --- | --- | --- |
| `destination_url` | yes | Backend + bucket + prefix, e.g. `gs://bucket/prefix/` or `s3://bucket/prefix/`. Scheme selects the backend. |
| `partition_prefix` | no | Partition to snapshot (`YYYYMMDD`). Defaults to **yesterday** UTC. |

**Example**

```sh
curl -sL -XPOST http://vlbackup:8080/snapshot \
  -H "Content-Type: application/json" \
  -d '{"destination_url": "s3://my-bucket/backups/", "partition_prefix": "20260703"}'
```

**Responses**

| Status | Meaning |
| --- | --- |
| `202 Accepted` | Snapshot(s) created and uploaded; body `OK`. |
| `204 No Content` | Snapshot created but VictoriaLogs returned no paths — nothing to upload. |
| `400 Bad Request` | Malformed body, unparseable `destination_url`, or an unsupported scheme. |
| `500 Internal Server Error` | VictoriaLogs, storage, or upload failure. Body is `{"error": "..."}`. |

Each returned snapshot path is uploaded as `<prefix>/<partition>.tar.gz`, then the snapshot is deleted from VictoriaLogs.

## `POST /api/v1/transfer`

Transfers per-day partitions from this ("source") VictoriaLogs to another ("target") instance running its own vlbackup sidecar. See [Partition Transfer](../user-guide/partition-transfer.md) for the full mechanism.

**Request body**

```json
{
  "target_url": "http://vlbackup-target:8080",
  "range": {
    "from": "2026-07-01T00:00:00Z",
    "to": "2026-07-03T00:00:00Z"
  }
}
```

| Field | Required | Description |
| --- | --- | --- |
| `target_url` | yes | Base URL of the target vlbackup sidecar. |
| `range.from` | yes | Start of the range (RFC 3339). |
| `range.to` | no | End of the range. Defaults to today. |

The range is interpreted as UTC days `[from, to)`. **Today's active partition is never transferred** — only sealed days strictly before today UTC are eligible.

**Example**

```sh
curl -sL -XPOST http://vlbackup-source:8080/api/v1/transfer \
  -H "Content-Type: application/json" \
  -d '{"target_url": "http://vlbackup-target:8080", "range": {"from": "2026-07-01T00:00:00Z"}}'
```

**Response**

```json
{"transferred": ["20260701"], "skipped": ["20260702"], "errors": []}
```

- `transferred` — days successfully moved to the target.
- `skipped` — days already present on the target (source data left untouched).
- `errors` — per-day failures.

Status is `200` when `errors` is empty, `500` otherwise (with the partial summary included).

## `POST /api/v1/transfer/receive` and `/attach`

These are the **target-side** endpoints, called by the source vlbackup — not by operators. They are protected by the shared bearer token when `TRANSFERAUTHKEY` is set.

- `/api/v1/transfer/receive?partition=YYYYMMDD` — accepts a `tar.gz` stream and extracts it under `<DATAPATH>/partitions/`.
- `/api/v1/transfer/attach?partition=YYYYMMDD` — attaches the received partition in VictoriaLogs.

If the source dies between detaching and attaching, the data is present but unattached on the target; recover manually:

```sh
curl -XPOST -H "Authorization: Bearer $TOKEN" \
  "http://vlbackup-target:8080/api/v1/transfer/attach?partition=20260702"
```

## Health and metrics

| Endpoint | Description |
| --- | --- |
| `GET /healthz` | Liveness. Returns `200` when the process is up. |
| `GET /readyz` | Readiness. Returns `200` when ready to serve. |
| `GET /metrics` | Prometheus exposition format. See [Metrics](metrics.md). |
