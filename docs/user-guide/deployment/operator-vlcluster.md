# Operator — VLCluster

The [VictoriaMetrics Kubernetes operator](https://docs.victoriametrics.com/operator/) deploys a VictoriaLogs cluster as a `VLCluster` custom resource with three components: `vlinsert` (write), `vlselect` (read) and `vlstorage`.

**Partition data lives only on `vlstorage`**, so the `vlbackup` sidecar goes under `spec.vlstorage.containers` — one per vlstorage StatefulSet pod. `vlinsert` and `vlselect` are stateless and need no sidecar.

The operator mounts the vlstorage data volume named `vlstorage-db` at `spec.vlstorage.storageDataPath` (default `/vlstorage-data`). The sidecar mounts that **same** volume at the **same** path. vlstorage serves HTTP on port `9491`, so the sidecar targets `http://127.0.0.1:9491`.

=== "GCS (`gs://`)"

    ```yaml
    apiVersion: operator.victoriametrics.com/v1
    kind: VLCluster
    metadata:
      name: victoria-logs
    spec:
      vlinsert:
        replicaCount: 2
      vlselect:
        replicaCount: 2
      vlstorage:
        replicaCount: 2
        retentionPeriod: "1y"
        storage:
          resources:
            requests:
              storage: 100Gi
        extraArgs:
          partitionManageAuthKey: ...
        volumes:
          - name: google-credentials
            secret:
              secretName: google-credentials
        containers:
          - name: snapshot
            image: ghcr.io/sbordeyne/vlbackup:v3.1.1
            args:
              - --victoria-logs-url=http://127.0.0.1:9491
              - --host=:8080
              - --ops-host=:9090
              - --data-path=/vlstorage-data
            env:
              - name: VLBACKUP_VICTORIA_LOGS_AUTH_KEY
                valueFrom:
                  secretKeyRef:
                    name: victoria-logs-secret
                    key: victoria-logs-auth-key
              - name: VLBACKUP_TRANSFER_AUTH_KEY
                valueFrom:
                  secretKeyRef:
                    name: victoria-logs-secret
                    key: transfer-auth-key
              - name: GOOGLE_APPLICATION_CREDENTIALS
                value: /var/secrets/google/key.json
            volumeMounts:
              - name: vlstorage-db
                mountPath: /vlstorage-data
              - name: google-credentials
                mountPath: /var/secrets/google
                readOnly: true
            ports:
              - name: http
                containerPort: 8080
                protocol: TCP
              - name: admin
                containerPort: 9090
                protocol: TCP
            readinessProbe:
              httpGet:
                path: /healthz
                port: admin
              initialDelaySeconds: 10
              periodSeconds: 30
              timeoutSeconds: 5
              failureThreshold: 3
              successThreshold: 1
            livenessProbe:
              httpGet:
                path: /healthz
                port: admin
              initialDelaySeconds: 10
              periodSeconds: 30
              timeoutSeconds: 5
              failureThreshold: 3
              successThreshold: 1
    ```

=== "S3 (`s3://`)"

    ```yaml
    apiVersion: operator.victoriametrics.com/v1
    kind: VLCluster
    metadata:
      name: victoria-logs
    spec:
      vlinsert:
        replicaCount: 2
      vlselect:
        replicaCount: 2
      vlstorage:
        replicaCount: 2
        retentionPeriod: "1y"
        storage:
          resources:
            requests:
              storage: 100Gi
        extraArgs:
          partitionManageAuthKey: ...
        containers:
          - name: snapshot
            image: ghcr.io/sbordeyne/vlbackup:v3.1.1
            args:
              - --victoria-logs-url=http://127.0.0.1:9491
              - --host=:8080
              - --ops-host=:9090
              - --data-path=/vlstorage-data
            env:
              - name: VLBACKUP_VICTORIA_LOGS_AUTH_KEY
                valueFrom:
                  secretKeyRef:
                    name: victoria-logs-secret
                    key: victoria-logs-auth-key
              - name: VLBACKUP_TRANSFER_AUTH_KEY
                valueFrom:
                  secretKeyRef:
                    name: victoria-logs-secret
                    key: transfer-auth-key
              - name: AWS_ACCESS_KEY_ID
                valueFrom:
                  secretKeyRef:
                    name: aws-credentials
                    key: access-key-id
              - name: AWS_SECRET_ACCESS_KEY
                valueFrom:
                  secretKeyRef:
                    name: aws-credentials
                    key: secret-access-key
              - name: S3_ENDPOINT
                valueFrom:
                  secretKeyRef:
                    name: aws-credentials
                    key: s3-endpoint
            volumeMounts:
              - name: vlstorage-db
                mountPath: /vlstorage-data
            ports:
              - name: http
                containerPort: 8080
                protocol: TCP
              - name: admin
                containerPort: 9090
                protocol: TCP
            readinessProbe:
              httpGet:
                path: /healthz
                port: admin
              initialDelaySeconds: 10
              periodSeconds: 30
              timeoutSeconds: 5
              failureThreshold: 3
              successThreshold: 1
            livenessProbe:
              httpGet:
                path: /healthz
                port: admin
              initialDelaySeconds: 10
              periodSeconds: 30
              timeoutSeconds: 5
              failureThreshold: 3
              successThreshold: 1
    ```

!!! warning "One sidecar per vlstorage pod"
    Each vlstorage replica holds a distinct shard of partitions. The sidecar snapshots **only the data on its own pod**, so a snapshot/transfer must be triggered against every vlstorage sidecar to cover the whole cluster. Address the per-pod sidecar via the headless StatefulSet service (`<name>-vlstorage-<ordinal>`).

!!! warning "Data path must match vlstorage"
    The sidecar `--data-path` and its `vlstorage-db` volume mount **must** equal `spec.vlstorage.storageDataPath` (default `/vlstorage-data`). If you override it, update both. See [Configuration](../configuration.md#-data-path-vlbackup_data_path).

- Credentials and `destination_url` schemes: [Object Storage](../object-storage.md).
- Set the same `VLBACKUP_TRANSFER_AUTH_KEY` on both sidecars for sidecar-to-sidecar [Partition Transfer](../partition-transfer.md).
- Trigger snapshots/transfers with a [CronJob](cronjob.md).
- For a single-node deployment, see [Operator — VLSingle](operator-vlsingle.md).
