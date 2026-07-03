# VLBackup

A go program to handle VictoriaLogs backups to Google Cloud Storage, and partition transfers between VictoriaLogs instances.

## CLI arguments

```txt
vlbackup v1.0.0
Usage: main [--host HOST] [--victorialogsurl VICTORIALOGSURL] [--victorialogsauthkey VICTORIALOGSAUTHKEY] [--datapath DATAPATH] [--transferauthkey TRANSFERAUTHKEY]

Options:
  --host HOST            The host to bind the HTTP server to [default: :8080, env: HOST]
  --victorialogsurl VICTORIALOGSURL
                         The VictoriaLogs URL [default: http://127.0.0.1:9428, env: VICTORIALOGSURL]
  --victorialogsauthkey VICTORIALOGSAUTHKEY
                         Optional auth key for victorialogs, use if VL -partitionManageAuthKey flag is set [env: VICTORIALOGSAUTHKEY]
  --datapath DATAPATH    Mount path of the VictoriaLogs data volume in this sidecar, must match VL -storageDataPath [default: /data, env: DATAPATH]
  --transferauthkey TRANSFERAUTHKEY
                         Optional shared bearer token for inter-vlbackup transfer endpoints [env: TRANSFERAUTHKEY]
  --help, -h             display this help and exit
  --version              display version and exit
```

## API

### `POST /snapshot`

Triggers a snapshot for the given partition prefix and the given destination

```sh
curl -sL -XPOST http://vlbackup:8080/snapshot -H "Content-Type: application/json" -d '{
  "destination_url": "gs://my-bucket/path/to/folder",
  "partition_prefix": "20060102"
}'
```

### `POST /api/v1/transfer`

Transfers per-day partitions from the local ("source") VictoriaLogs instance to another ("target") VictoriaLogs instance running its own vlbackup sidecar. Intended to be triggered by a Kubernetes CronJob to move sealed partitions from fast/small storage to slow/large storage.

```sh
curl -sL -XPOST http://vlbackup-source:8080/api/v1/transfer -H "Content-Type: application/json" -d '{
  "target_url": "http://vlbackup-target:8080",
  "range": {
    "from": "2026-07-01T00:00:00Z",
    "to": "2026-07-03T00:00:00Z"
  }
}'
```

- `range.from` is required, `range.to` is optional and defaults to today.
- The range is interpreted as UTC days `[from, to)`. **Today's (active) partition is never transferred** — only sealed days strictly before today UTC are eligible.
- For each day, the source snapshots the partition, streams it as tar.gz to the target's `/api/v1/transfer/receive`, deletes the snapshot, detaches the partition locally, then asks the target to attach it via `/api/v1/transfer/attach`.
- If a partition already exists on the target, that day is **skipped** (source data untouched) and the transfer continues.
- Any other error aborts the remaining days.

Response:

```json
{"transferred": ["20260701"], "skipped": ["20260702"], "errors": []}
```

Status is `200` when there are no errors, `500` otherwise (with the partial summary included).

The target-side endpoints `/api/v1/transfer/receive` and `/api/v1/transfer/attach` are called by the source vlbackup, not by operators. They are protected by a shared bearer token when `TRANSFERAUTHKEY` is set (set the same value on both sidecars). If the source dies between detach and attach, the data is present but unattached on the target: recover with `curl -XPOST -H "Authorization: Bearer $TOKEN" http://vlbackup-target:8080/api/v1/transfer/attach?partition=YYYYMMDD`.

**Deployment requirements for transfers:**

- Both vlbackup sidecars must mount their VictoriaLogs data volume **at the same path as VictoriaLogs itself** (`-storageDataPath`, default `/data`, configured via `DATAPATH`). The source reads snapshot files at the paths VictoriaLogs reports; the target writes partitions into `<DATAPATH>/partitions/`.
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
        - --victorialogsurl=http://localhost:9428
        - --victorialogsauthkey=$(VICTORIA_LOGS_AUTH_KEY)
      ports:
        - containerPort: 8080
          name: http-snapshot
          protocol: TCP
      readinessProbe:
        httpGet:
          path: /healthz
          port: http
        initialDelaySeconds: 10
        periodSeconds: 30
        timeoutSeconds: 5
        failureThreshold: 3
        successThreshold: 1
      livenessProbe:
        httpGet:
          path: /healthz
          port: http
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

Endpoint available on `/metrics`

| **name**                             | **type**  | **labels**         |
| ------------------------------------ | --------- | ------------------ |
| `vlbackup_snapshot_duration_seconds` | HISTOGRAM | snapshot, stage    |
| `vlbackup_snapshot_count`            | COUNTER   | snapshot, success  |
| `vlbackup_transfer_duration_seconds` | HISTOGRAM | partition, stage   |
| `vlbackup_transfer_count`            | COUNTER   | partition, result  |
| `vlbackup_transfer_bytes_total`      | COUNTER   | direction          |
