# CronJob

You can drive vlbackup on a schedule with a Kubernetes CronJob that runs the
[`vlbackupctl`](../../reference/cli.md) image. Each job invokes a subcommand
(`snapshot`, `transfer`, `migrate`) against the vlbackup API and exits non-zero if
the call fails, so a failed backup surfaces as a failed Job.

=== "GCS"

    Incremental backups to GCS of the last **sealed** day of data.

    ```yaml
    apiVersion: batch/v1
    kind: CronJob
    metadata:
      name: trigger-snapshots
    spec:
      concurrencyPolicy: Forbid
      schedule: "0 9 * * *"  # Runs every day at 9 AM
      jobTemplate:
        spec:
          backoffLimit: 0
          activeDeadlineSeconds: 300
          completions: 1
          parallelism: 1
          template:
            metadata:
              labels:
                app: victorialogs
            spec:
              serviceAccountName: victorialogs
              restartPolicy: Never
              terminationGracePeriodSeconds: 10
              containers:
                - name: vlbackupctl
                  image: ghcr.io/sbordeyne/vlbackupctl:latest
                  args:
                    - snapshot
                    - --url=http://victorialogs:8080
                    - --from=now-2d/d
                    - --to=now-1d/d
                    - --dest-url=gs://my-bucket/path/to/backups
    ```

=== "S3"

    Incremental backups to S3 of the last **sealed** day of data.

    ```yaml
    apiVersion: batch/v1
    kind: CronJob
    metadata:
      name: trigger-snapshots
    spec:
      concurrencyPolicy: Forbid
      schedule: "0 9 * * *"  # Runs every day at 9 AM
      jobTemplate:
        spec:
          backoffLimit: 0
          activeDeadlineSeconds: 300
          completions: 1
          parallelism: 1
          template:
            metadata:
              labels:
                app: victorialogs
            spec:
              serviceAccountName: victorialogs
              restartPolicy: Never
              terminationGracePeriodSeconds: 10
              containers:
                - name: vlbackupctl
                  image: ghcr.io/sbordeyne/vlbackupctl:latest
                  args:
                    - snapshot
                    - --url=http://victorialogs:8080
                    - --from=now-2d/d
                    - --to=now-1d/d
                    - --dest-url=s3://my-bucket/path/to/backups
    ```

=== "Transfer"

    Transfers all of the data up until the last 15 days to a "slow" instance. You should tune this according to the primary and secondary `retentionPeriod` settings.

    ```yaml
    apiVersion: batch/v1
    kind: CronJob
    metadata:
      name: trigger-transfer
    spec:
      concurrencyPolicy: Forbid
      schedule: "0 9 * * *"  # Runs every day at 9 AM
      jobTemplate:
        spec:
          backoffLimit: 0
          activeDeadlineSeconds: 300
          completions: 1
          parallelism: 1
          template:
            metadata:
              labels:
                app: victorialogs
            spec:
              serviceAccountName: victorialogs
              restartPolicy: Never
              terminationGracePeriodSeconds: 10
              containers:
                - name: vlbackupctl
                  image: ghcr.io/sbordeyne/vlbackupctl:latest
                  args:
                    - transfer
                    - --url=http://victorialogs:8080
                    - --from=now-1y
                    - --to=now-15d/d
                    - --target-url=http://victorialogs-slow:8080
    ```

=== "Migrate"

    Like transfer, but also copies **today's still-open data** to the target (see
    [Partition Migrate](../partition-migrate.md)). Migrate reaches the target's
    vlbackup, insert and select APIs directly, so it needs all three URLs.

    !!! warning "Migrate is at-least-once"
        Re-running migrate **re-inserts today's rows** on the target — VictoriaLogs
        does not deduplicate on ingest. Schedule it to run once per target, or
        expect duplicated rows for the current day. See [Partition Migrate](../partition-migrate.md).

    ```yaml
    apiVersion: batch/v1
    kind: CronJob
    metadata:
      name: trigger-migrate
    spec:
      concurrencyPolicy: Forbid
      schedule: "0 9 * * *"  # Runs every day at 9 AM
      jobTemplate:
        spec:
          backoffLimit: 0
          activeDeadlineSeconds: 300
          completions: 1
          parallelism: 1
          template:
            metadata:
              labels:
                app: victorialogs
            spec:
              serviceAccountName: victorialogs
              restartPolicy: Never
              terminationGracePeriodSeconds: 10
              containers:
                - name: vlbackupctl
                  image: ghcr.io/sbordeyne/vlbackupctl:latest
                  args:
                    - migrate
                    - --url=http://victorialogs:8080
                    - --from=now-1y
                    - --to=now-15d/d
                    - --target-vlbackup-url=http://victorialogs-slow:8080
                    - --target-vlinsert-url=http://victorialogs-slow:9428
                    - --target-vlselect-url=http://victorialogs-slow:9428
    ```

!!! tip "Long-running jobs"
    `transfer` and `migrate` can take a while over large ranges. `vlbackupctl`
    defaults to a `30m` HTTP timeout (`--timeout` / `VLBACKUPCTL_TIMEOUT`); raise
    it and `activeDeadlineSeconds` together if a job needs longer.

See the [vlbackupctl CLI reference](../../reference/cli.md) for every subcommand and flag.
