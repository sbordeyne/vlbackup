# Configuration

VLBackup is configured through command-line flags. Every flag also has an environment-variable equivalent, so it works equally well in a shell, a container, or a Kubernetes manifest.

## CLI flags and environment variables

| Flag                       | Env var                           | Default                 | Description                                                                                         |
| -------------------------- | --------------------------------- | ----------------------- | --------------------------------------------------------------------------------------------------- |
| `--host`                   | `VLBACKUP_HOST`                   | `:8080`                 | Address the HTTP server binds to.                                                                   |
| `--victoria-logs-url`      | `VLBACKUP_VICTORIA_LOGS_URL`      | `http://127.0.0.1:9428` | Base URL of the VictoriaLogs instance to snapshot/transfer.                                         |
| `--victoria-logs-auth-key` | `VLBACKUP_VICTORIA_LOGS_AUTH_KEY` | *(empty)*               | Auth key for VictoriaLogs; set it if VL runs with `-partitionManageAuthKey`.                        |
| `--data-path`              | `VLBACKUP_DATA_PATH`              | `/data`                 | Mount path of the VictoriaLogs data volume in this sidecar. **Must match** VL's `-storageDataPath`. |
| `--transfer-auth-key`      | `VLBACKUP_TRANSFER_AUTH_KEY`      | *(empty)*               | Shared bearer token protecting the inter-vlbackup transfer endpoints.                               |

!!! note "Storage backend configuration is separate"
    Object-storage credentials and endpoints are **not** set through these flags — they are read from backend-specific environment variables (`GOOGLE_APPLICATION_CREDENTIALS`, `AWS_*`, `S3_ENDPOINT`, …). See [Object Storage](object-storage.md).

## `--help`

```txt
vlbackup v1.0.0
Usage: vlbackup [--host HOST] [--victoria-logs-url VICTORIA-LOGS-URL] [--victoria-logs-auth-key VICTORIA-LOGS-AUTH-KEY] [--data-path DATA-PATH] [--transfer-auth-key TRANSFER-AUTH-KEY]

Options:
  --host HOST            The host to bind the HTTP server to [default: :8080, env: VLBACKUP_HOST]
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

## Notes on specific settings

### `--data-path` / `VLBACKUP_DATA_PATH`

VictoriaLogs reports snapshot paths relative to its own `-storageDataPath`. vlbackup reads those files directly from the shared volume, so `VLBACKUP_DATA_PATH` must point at the **same** volume mounted at the **same** path. On the transfer target side, received partitions are written under `<data-path>/partitions/`.

### `--transfer-auth-key` / `VLBACKUP_TRANSFER_AUTH_KEY`

Only the transfer *target* endpoints (`/api/v1/transfer/receive` and `/api/v1/transfer/attach`) are protected by this bearer token. Set the **same value** on both the source and target sidecars. When empty, the endpoints are unauthenticated — acceptable only on a trusted network. See [Partition Transfer](partition-transfer.md).
