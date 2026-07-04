# Deployment

VLBackup is intended to run as a **sidecar** to VictoriaLogs storage nodes, sharing the data volume. It is easiest to use with a VictoriaLogs single deployment.

## Container image

Multi-arch images (`linux/amd64`, `linux/arm64`) are published to GitHub Container Registry, built from a distroless base:

```text
ghcr.io/sbordeyne/vlbackup:latest
ghcr.io/sbordeyne/vlbackup:v1.0.2
```

Releases are cut by [GoReleaser](https://goreleaser.com/) whenever a `v*` tag is pushed (see `.goreleaser.yaml` and `.github/workflows/release.yaml`), which also publishes `tar.gz` binary archives.

## Helm sidecar example

Example `victoria-logs-single` Helm chart values that add vlbackup as a sidecar and mount a GCS service-account key:

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

!!! warning "Match the data path"
    The sidecar's `VLBACKUP_DATA_PATH` (default `/data`) must point at the **same volume, at the same mount path** VictoriaLogs uses for `-storageDataPath`. Adjust `volumeMounts` and `--data-path` accordingly.

## Credentials

Which environment variables to inject depends on the backend (see [Object Storage](../user-guide/object-storage.md)):

=== "GCS (`gs://`)"

    Mount a service-account key and point ADC at it:

    ```yaml
    env:
      - name: GOOGLE_APPLICATION_CREDENTIALS
        value: /var/secrets/google/key.json
    ```

    Or rely on Workload Identity and set nothing.

=== "S3 (`s3://`)"

    Inject credentials and endpoint from a secret:

    ```yaml
    env:
      - name: AWS_ACCESS_KEY_ID
        valueFrom: { secretKeyRef: { name: s3-creds, key: access-key-id } }
      - name: AWS_SECRET_ACCESS_KEY
        valueFrom: { secretKeyRef: { name: s3-creds, key: secret-access-key } }
      - name: S3_ENDPOINT
        value: s3.us-east-1.amazonaws.com
    ```

## Triggering backups and transfers

vlbackup does not schedule anything itself — drive it from a Kubernetes `CronJob` (or any scheduler) that `POST`s to `/snapshot` and/or `/api/v1/transfer`. See the [HTTP API](http-api.md) and [Partition Transfer](../user-guide/partition-transfer.md).
