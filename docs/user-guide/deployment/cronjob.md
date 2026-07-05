# CronJob

You can then add a kubernetes CronJob to trigger snapshots or transfers between 2 victorialogs instances

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
                - name: curl
                  image: curlimages/curl:8.21.0
                  args:
                    - "--location"
                    - "--silent"
                    - "-XPOST"
                    - "http://victorialogs:8080/v1/vlbackup/snapshot"
                    - "-H 'Content-Type: application/json'"
                    - >-
                      -d '{"destination_url": "gs://my-bucket/path/to/backups", "range":{"from": "now-2d/d", "to": "now-1d/d"}}'"
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
                - name: curl
                  image: curlimages/curl:8.21.0
                  args:
                    - "--location"
                    - "--silent"
                    - "-XPOST"
                    - "http://victorialogs:8080/v1/vlbackup/snapshot"
                    - "-H 'Content-Type: application/json'"
                    - >-
                      -d '{"destination_url": "s3://my-bucket/path/to/backups", "range":{"from": "now-2d/d", "to": "now-1d/d"}}'"
    ```

=== "Transfer"

    Transfers all of the data up until the last 15 days to a "slow" instance. You should tune this according to the primary and secondary `retentionPeriod` settings.

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
                - name: curl
                  image: curlimages/curl:8.21.0
                  args:
                    - "--location"
                    - "--silent"
                    - "-XPOST"
                    - "http://victorialogs:8080/v1/vlbackup/transfer"
                    - "-H 'Content-Type: application/json'"
                    - >-
                      -d '{"target_url": "http://victorialogs-slow:8080", "range":{"from": "now-1y", "to": "now-15d/d"}}'"
    ```
