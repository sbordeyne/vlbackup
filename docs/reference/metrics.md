# Metrics

VLBackup exposes Prometheus metrics on `GET /metrics`, alongside the standard Go runtime and process collectors and a `vlbackup` build-info metric.

## Application metrics

| Name | Type | Labels | Description |
| --- | --- | --- | --- |
| `vlbackup_snapshot_duration_seconds` | Histogram | `snapshot`, `stage` | Duration of snapshot stages. Uses default Prometheus buckets. |
| `vlbackup_snapshot_count` | Counter | `snapshot`, `success` | Snapshots performed, labelled by success (`true`/`false`). |
| `vlbackup_transfer_duration_seconds` | Histogram | `partition`, `stage` | Duration of partition-transfer stages. Exponential buckets (~0.1s to ~13min), since streams can take minutes. |
| `vlbackup_transfer_count` | Counter | `partition`, `result` | Partition transfers by result (`transferred`, `skipped`, `error`). |
| `vlbackup_transfer_bytes_total` | Counter | `direction` | Bytes transferred between vlbackup instances, by `direction` (`sent`, `received`). |

## Label values

- `snapshot` / `partition` — the partition prefix (`YYYYMMDD`) the operation concerns.
- `stage` — the sub-step being timed (e.g. `parse_request_body`).
- `success` — `true` or `false`.
- `result` — `transferred`, `skipped`, or `error`.
- `direction` — `sent` or `received`.

## Scraping

Point Prometheus at the sidecar's `/metrics` endpoint. When running as a Kubernetes sidecar, expose the container port (default `8080`) and add the usual scrape annotations or a `ServiceMonitor`/`PodMonitor` targeting it.
