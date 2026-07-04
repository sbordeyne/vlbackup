# VLBackup

[![Docs](https://img.shields.io/badge/docs-GitHub%20Pages-blue?logo=materialformkdocs&logoColor=white)](https://sbordeyne.github.io/vlbackup)
[![CI](https://img.shields.io/github/actions/workflow/status/sbordeyne/vlbackup/pr.yaml?branch=master&label=CI)](https://github.com/sbordeyne/vlbackup/actions/workflows/pr.yaml)
[![Coverage](https://raw.githubusercontent.com/sbordeyne/vlbackup/badges/.badges/master/coverage.svg)](https://github.com/sbordeyne/vlbackup/actions/workflows/pr.yaml)
[![Go Report Card](https://goreportcard.com/badge/github.com/sbordeyne/vlbackup)](https://goreportcard.com/report/github.com/sbordeyne/vlbackup)
[![Release](https://img.shields.io/github/v/release/sbordeyne/vlbackup)](https://github.com/sbordeyne/vlbackup/releases/latest)
[![Go version](https://img.shields.io/github/go-mod/go-version/sbordeyne/vlbackup)](go.mod)

A go program to handle VictoriaLogs backups to object storage (Google Cloud Storage or any S3-compatible store), and partition transfers between VictoriaLogs instances.

## CLI arguments

```txt
vlbackup v1.0.0
Usage: vlbackup [--host HOST] [--ops-host OPS-HOST] [--victoria-logs-url VICTORIA-LOGS-URL] [--victoria-logs-auth-key VICTORIA-LOGS-AUTH-KEY] [--data-path DATA-PATH] [--transfer-auth-key TRANSFER-AUTH-KEY]

Options:
  --host HOST            The host to bind the HTTP server to [default: :8080, env: VLBACKUP_HOST]
  --ops-host OPS-HOST    The host to bind the health/ready/metrics server to [default: :9090, env: VLBACKUP_OPS_HOST]
  --victoria-logs-url VICTORIA-LOGS-URL
                         The VictoriaLogs URL [default: http://127.0.0.1:9428, env: VLBACKUP_VICTORIA_LOGS_URL]
  --victoria-logs-auth-key VICTORIA-LOGS-AUTH-KEY
                         Optional auth key for victorialogs, use if VL -partitionManageAuthKey flag is set [env: VLBACKUP_VICTORIA_LOGS_AUTH_KEY]
  --data-path DATA-PATH
                         Mount path of the VictoriaLogs data volume in this sidecar, must match VL -storageDataPath [default: /data, env: VLBACKUP_DATA_PATH]
  --transfer-auth-key TRANSFER-AUTH-KEY
                         Optional shared bearer token for inter-vlbackup transfer endpoints [env: VLBACKUP_TRANSFER_AUTH_KEY]
  --help, -h             display this help and exit
  --version              display version and exit
```

## API

The API is served on `--host` (default `:8080`). Health, readiness and metrics are on a separate ops server (`--ops-host`, default `:9090`). The API is schema-first: [`openapi/schema.yaml`](openapi/schema.yaml) is the source of truth, and the full reference is rendered as an interactive [API Explorer](https://sbordeyne.github.io/vlbackup/reference/api-explorer/) on the docs site.

### `POST /v1/vlbackup/snapshot`

Triggers a snapshot for the given partition prefix and uploads it to the given destination

```sh
curl -sL -XPOST http://vlbackup:8080/v1/vlbackup/snapshot -H "Content-Type: application/json" -d '{
  "destination_url": "gs://my-bucket/path/to/folder",
  "partition_prefix": "20060102"
}'
```

The storage backend is selected from the `destination_url` scheme (see [Object storage backends](#object-storage-backends)). Each snapshot is streamed as one tar.gz object named `<pathprefix>/<partition>.tar.gz` (e.g. `path/to/folder/20060102.tar.gz`); re-running a snapshot for the same partition overwrites the object.

## Object storage backends

The destination URL scheme picks the backend; bucket name is the URL host, and the URL path is used as a key prefix. Backends are configured through environment variables only.

### `gs://` — Google Cloud Storage

Uses Application Default Credentials.

| env var | description |
| --- | --- |
| `GOOGLE_APPLICATION_CREDENTIALS` | Path to a service-account key file (or use workload identity) |
| `STORAGE_EMULATOR_HOST` | Optional `host:port` of a GCS emulator such as fake-gcs-server |

### `s3://` — AWS S3 and S3-compatible stores (MinIO, Ceph, R2, ...)

| env var | description |
| --- | --- |
| `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` | Access credentials |
| `AWS_SESSION_TOKEN` | Optional session token |
| `AWS_REGION` | Optional region (auto-detected when unset) |
| `S3_ENDPOINT` | `host[:port]` of the endpoint, default `s3.amazonaws.com` |
| `S3_USE_SSL` | `true`/`false`, default `true` |

Adding another backend is a single file in `pkg/objstore` implementing the `Repository` interface and registering its URL scheme via `objstore.Register` in `init()`.

### `POST /v1/vlbackup/transfer`

Transfers per-day partitions from the local ("source") VictoriaLogs instance to another ("target") VictoriaLogs instance running its own vlbackup sidecar. Intended to be triggered by a Kubernetes CronJob to move sealed partitions from fast/small storage to slow/large storage.

```sh
curl -sL -XPOST http://vlbackup-source:8080/v1/vlbackup/transfer -H "Content-Type: application/json" -d '{
  "target_url": "http://vlbackup-target:8080",
  "range": {
    "from": "2026-07-01T00:00:00Z",
    "to": "2026-07-03T00:00:00Z"
  }
}'
```

- `range.from` is required, `range.to` is optional and defaults to today.
- The range is interpreted as UTC days `[from, to)`. **Today's (active) partition is never transferred** — only sealed days strictly before today UTC are eligible.
- For each day, the source snapshots the partition, streams it as tar.gz to the target's `/v1/vlbackup/transfer/receive`, deletes the snapshot, detaches the partition locally, then asks the target to attach it via `/v1/vlbackup/transfer/attach`.
- If a partition already exists on the target, that day is **skipped** (source data untouched) and the transfer continues.
- Any other error aborts the remaining days.

Response:

```json
{"transferred": ["20260701"], "skipped": ["20260702"], "errors": []}
```

Status is `200` when there are no errors, `500` otherwise (with the partial summary included).

The target-side endpoints `/v1/vlbackup/transfer/receive` and `/v1/vlbackup/transfer/attach` are called by the source vlbackup, not by operators. They are protected by a shared bearer token when `VLBACKUP_TRANSFER_AUTH_KEY` is set (set the same value on both sidecars). If the source dies between detach and attach, the data is present but unattached on the target: recover with `curl -XPOST -H "Authorization: Bearer $TOKEN" http://vlbackup-target:8080/v1/vlbackup/transfer/attach?partition=YYYYMMDD`.

### `POST /v1/vlbackup/restore`

Downloads a partition snapshot from object storage, extracts it into the local data path, and attaches it to VictoriaLogs.

```sh
curl -sL -XPOST http://vlbackup:8080/v1/vlbackup/restore -H "Content-Type: application/json" -d '{
  "source_url": "gs://my-bucket/path/to/folder",
  "partition_prefix": "20060102"
}'
```

Both `source_url` and `partition_prefix` are required. Returns `404` if the snapshot is missing in object storage, `409` if the partition is already attached locally.

**Deployment requirements for transfers:**

- Both vlbackup sidecars must mount their VictoriaLogs data volume **at the same path as VictoriaLogs itself** (`-storageDataPath`, default `/data`, configured via `VLBACKUP_DATA_PATH`). The source reads snapshot files at the paths VictoriaLogs reports; the target writes partitions into `<data-path>/partitions/`.
- VictoriaLogs must be recent enough to expose the `/internal/partition/*` endpoints (snapshot create/delete, attach, detach).

## Deployment

This is intended to be deployed as a sidecar to victorialogs storage nodes. It is easier to use on a VictoriaLogs single deployment.
Example `victoria-logs-single` helm chart values:

```yaml
server:
  extraVolumes:
    - name: google-credentials
      secret:
        secretName: backup-credentials
  extraContainers:
    - name: snapshot
      image: ghcr.io/sbordeyne/vlbackup:v1.0.2
      args:
        - --victoria-logs-url=http://localhost:9428
        - --victoria-logs-auth-key=$(VICTORIA_LOGS_AUTH_KEY)
      ports:
        - containerPort: 8080
          name: http-api
          protocol: TCP
        - containerPort: 9090
          name: http-ops
          protocol: TCP
      readinessProbe:
        httpGet:
          path: /readyz
          port: http-ops
        initialDelaySeconds: 10
        periodSeconds: 30
        timeoutSeconds: 5
        failureThreshold: 3
        successThreshold: 1
      livenessProbe:
        httpGet:
          path: /healthz
          port: http-ops
        initialDelaySeconds: 10
        periodSeconds: 30
        timeoutSeconds: 5
        failureThreshold: 3
        successThreshold: 1
      env:
        - name: VICTORIA_LOGS_AUTH_KEY
          valueFrom:
            secretKeyRef:
              name: victorialogs-secret
              key: auth-key
        - name: GOOGLE_APPLICATION_CREDENTIALS
          value: /var/secrets/google/key.json
      volumeMounts:
        - name: server-volume
          mountPath: /storage
        - name: google-credentials
          mountPath: /var/secrets/google
          readOnly: true
```

## Metrics

Endpoint available on `/metrics`, served by the ops server on `--ops-host` (default `:9090`).

| **name**                             | **type**  | **labels**         |
| ------------------------------------ | --------- | ------------------ |
| `vlbackup_snapshot_duration_seconds` | HISTOGRAM | snapshot, stage    |
| `vlbackup_snapshot_count`            | COUNTER   | snapshot, success  |
| `vlbackup_transfer_duration_seconds` | HISTOGRAM | partition, stage   |
| `vlbackup_transfer_count`            | COUNTER   | partition, result  |
| `vlbackup_transfer_bytes_total`      | COUNTER   | direction          |
