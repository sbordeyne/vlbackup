# VLBackup

A go program to handle VictoriaLogs backups to Google Cloud Storage

## CLI arguments

```txt
vlbackup v1.0.0
Usage: main [--host HOST] [--victorialogsurl VICTORIALOGSURL] [--victorialogsauthkey VICTORIALOGSAUTHKEY]

Options:
  --host HOST            The host to bind the HTTP server to [default: :8080, env: HOST]
  --victorialogsurl VICTORIALOGSURL
                         The VictoriaLogs URL [default: http://127.0.0.1:9428, env: VICTORIALOGSURL]
  --victorialogsauthkey VICTORIALOGSAUTHKEY
                         Optional auth key for victorialogs, use if VL -partitionManageAuthKey flag is set [env: VICTORIALOGSAUTHKEY]
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

| **name**                             | **type**  | **labels**        |
| ------------------------------------ | --------- | ----------------- |
| `vlbackup_snapshot_duration_seconds` | HISTOGRAM | snapshot, stage   |
| `vlbackup_snapshot_count`            | COUNTER   | snapshot, success |
