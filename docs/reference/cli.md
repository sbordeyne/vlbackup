# vlbackupctl CLI

`vlbackupctl` is a small command-line client for the vlbackup [HTTP API](http-api.md). It turns each API endpoint into a subcommand, so you can drive snapshots, transfers, migrations and restores from a shell or a Kubernetes [CronJob](../user-guide/deployment/cronjob.md) without hand-writing `curl` calls and JSON bodies.

It ships as its own container image, `ghcr.io/sbordeyne/vlbackupctl`, tagged `latest` and `vX.Y.Z` per release. It is a stateless client: point it at a vlbackup instance with `--url` and it makes a single API call per invocation, printing the result and exiting non-zero on failure.

## Global flags and environment variables

These apply to every subcommand.

| Flag             | Env var               | Default                 | Description                                                     |
| ---------------- | --------------------- | ----------------------- | --------------------------------------------------------------- |
| `--url`          | `VLBACKUPCTL_URL`     | `http://127.0.0.1:8080` | Base URL of the vlbackup API to call.                           |
| `--timeout`      | `VLBACKUPCTL_TIMEOUT` | `30m`                   | HTTP client timeout. Raise it for large transfers.              |
| `--output`, `-o` | `VLBACKUPCTL_OUTPUT`  | `text`                  | Output format: `text` (human summary) or `json` (raw response). |
| `--version`      | —                     | —                       | Print the version and exit.                                     |

!!! note "Ranges use vlbackup time expressions"
    `--from` / `--to` accept the same time expressions as the API — e.g. `now-7d/d`, `now/d`, or an RFC3339 timestamp. `--to` is optional and defaults to now. See [TimeRange](http-api.md) semantics.

## Subcommands

### `snapshot`

Snapshot each sealed day in the range to Object Storage (`POST /v1/vlbackup/snapshot`).

| Flag         | Required | Description                                                                      |
| ------------ | -------- | -------------------------------------------------------------------------------- |
| `--from`     | yes      | Start of the range, inclusive (time expression, e.g. `now-7d/d`).                |
| `--to`       | no       | End of the range, inclusive (defaults to now).                                   |
| `--dest-url` | yes      | Object Storage destination, e.g. `gs://bucket/prefix/` or `s3://bucket/prefix/`. |

```sh
vlbackupctl --url http://victorialogs:8080 snapshot \
  --from now-2d/d --to now-1d/d \
  --dest-url gs://my-bucket/path/to/backups
```

```text
Snapshot request accepted.
```

### `transfer`

Move each sealed day in the range to another vlbackup instance (`POST /v1/vlbackup/transfer`). See [Partition Transfer](../user-guide/partition-transfer.md).

| Flag           | Required | Description                                                                   |
| -------------- | -------- | ----------------------------------------------------------------------------- |
| `--from`       | yes      | Start of the range, inclusive.                                                |
| `--to`         | no       | End of the range, inclusive (defaults to now).                                |
| `--target-url` | yes      | Base URL of the target vlbackup instance, e.g. `http://target-vlbackup:8080`. |

```sh
vlbackupctl --url http://victorialogs:8080 transfer \
  --from now-1y --to now-15d/d \
  --target-url http://victorialogs-slow:8080
```

```text
transferred: [20240113 20240114]
skipped:     [20240112]
errors:      []
```

### `migrate`

Like `transfer`, but also copies today's still-open data to the target (`POST /v1/vlbackup/migrate`). See [Partition Migrate](../user-guide/partition-migrate.md).

| Flag                    | Required | Description                                                                                     |
| ----------------------- | -------- | ----------------------------------------------------------------------------------------------- |
| `--from`                | yes      | Start of the sealed-day range, inclusive.                                                       |
| `--to`                  | no       | End of the range, inclusive (defaults to now).                                                  |
| `--target-vlbackup-url` | yes      | Target vlbackup instance (receive/attach) for sealed partitions.                                |
| `--target-vlinsert-url` | yes      | Target VictoriaLogs insert API, for today's data.                                               |
| `--target-vlselect-url` | yes      | Target VictoriaLogs select API, used to verify row counts.                                      |
| `--target-vl-auth-key`  | no       | Auth key for the target VictoriaLogs insert/select APIs (env `VLBACKUPCTL_TARGET_VL_AUTH_KEY`). |

```sh
vlbackupctl --url http://victorialogs:8080 migrate \
  --from now-1y --to now-15d/d \
  --target-vlbackup-url http://victorialogs-slow:8080 \
  --target-vlinsert-url http://victorialogs-slow:9428 \
  --target-vlselect-url http://victorialogs-slow:9428
```

```text
transferred: [20240113 20240114]
skipped:     [20240112]
errors:      []
recent:      partition=20240115 bytes_ingested=4194304 source_count=1024 target_count=1024 verified=true
```

!!! warning "Migrate is at-least-once"
    Re-running migrate re-inserts today's rows on the target. Run it once per target. See [Partition Migrate](../user-guide/partition-migrate.md).

### `restore`

Download a partition snapshot from Object Storage and attach it locally (`POST /v1/vlbackup/restore`). Restore targets a single partition, not a range.

| Flag                 | Required | Description                                                                 |
| -------------------- | -------- | --------------------------------------------------------------------------- |
| `--partition-prefix` | yes      | Partition to restore, formatted `YYYYMMDD`.                                 |
| `--source-url`       | yes      | Object Storage source, e.g. `gs://bucket/prefix/` or `s3://bucket/prefix/`. |

```sh
vlbackupctl --url http://victorialogs:8080 restore \
  --partition-prefix 20240115 \
  --source-url gs://my-bucket/path/to/backups
```

```text
restored partitions: [20240115] (4194304 bytes written)
```

## Output and exit codes

- On success the command prints a human summary (`text`) or the raw API response (`--output json`) and exits `0`.
- On an API error (a non-2xx response) it prints the error message to stderr and exits non-zero, so a failed CronJob is reported as a failed Job. `transfer` and `migrate` also exit non-zero when the API returns per-day errors, after printing the partial result.

```sh
vlbackupctl --url http://victorialogs:8080 -o json transfer --from now-7d/d --target-url http://victorialogs-slow:8080
```

```json
{
  "transferred": ["20240113"],
  "skipped": ["20240112"],
  "errors": []
}
```

## `--help`

```txt
vlbackupctl 0.0.0-dev
Usage: vlbackupctl [--url URL] [--timeout TIMEOUT] [--output OUTPUT] <command> [<args>]

Options:
  --url URL              Base URL of the vlbackup API [default: http://127.0.0.1:8080, env: VLBACKUPCTL_URL]
  --timeout TIMEOUT      HTTP client timeout [default: 30m, env: VLBACKUPCTL_TIMEOUT]
  --output OUTPUT, -o OUTPUT
                         Output format: text or json [default: text, env: VLBACKUPCTL_OUTPUT]
  --help, -h             display this help and exit
  --version              display version and exit

Commands:
  snapshot               Snapshot a range of partitions to Object Storage
  restore                Restore a partition from Object Storage
  transfer               Transfer partitions to another vlbackup instance
  migrate                Migrate partitions and recent data to another vlbackup instance
```

Pass `--help` to any subcommand to see its own flags, e.g. `vlbackupctl migrate --help`.
