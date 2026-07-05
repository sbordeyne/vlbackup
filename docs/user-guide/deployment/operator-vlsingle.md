# Operator — VLSingle

The [VictoriaMetrics Kubernetes operator](https://docs.victoriametrics.com/operator/) manages VictoriaLogs through the `VLSingle` custom resource. `vlbackup` runs as a sidecar (`spec.containers`) that shares the VictoriaLogs data volume.

The operator mounts the data volume named `data` at `spec.storageDataPath` (default `/victoria-logs-data`). The sidecar mounts that **same** volume at the **same** path and sets `--data-path` to it. VLSingle serves HTTP on port `9428`, so the sidecar targets `http://127.0.0.1:9428`.

=== "GCS (`gs://`)"

    ```yaml
    apiVersion: operator.victoriametrics.com/v1
    kind: VLSingle
    metadata:
      name: victoria-logs
    spec:
      retentionPeriod: "12"
      storage:
        resources:
          requests:
            storage: 50Gi
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
            - --victoria-logs-url=http://127.0.0.1:9428
            - --host=:8080
            - --ops-host=:9090
            - --data-path=/victoria-logs-data
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
            - name: data
              mountPath: /victoria-logs-data
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
    kind: VLSingle
    metadata:
      name: victoria-logs
    spec:
      retentionPeriod: "12"
      storage:
        resources:
          requests:
            storage: 50Gi
      extraArgs:
        partitionManageAuthKey: ...
      containers:
        - name: snapshot
          image: ghcr.io/sbordeyne/vlbackup:v3.1.1
          args:
            - --victoria-logs-url=http://127.0.0.1:9428
            - --host=:8080
            - --ops-host=:9090
            - --data-path=/victoria-logs-data
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
            - name: data
              mountPath: /victoria-logs-data
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

!!! warning "Data path must match VictoriaLogs"
    The sidecar `--data-path` and its `data` volume mount **must** equal `spec.storageDataPath` (default `/victoria-logs-data`). If you override `spec.storageDataPath`, update both. See [Configuration](../configuration.md#-data-path-vlbackup_data_path).

- Credentials and `destination_url` schemes: [Object Storage](../object-storage.md).
- Set the same `VLBACKUP_TRANSFER_AUTH_KEY` on both sidecars for sidecar-to-sidecar [Partition Transfer](../partition-transfer.md).
- Trigger snapshots/transfers with a [CronJob](cronjob.md).
- For the non-operator Helm sidecar, see [Helm Chart](helm-chart.md).
- To back up a cluster deployment instead, see [Operator — VLCluster](operator-vlcluster.md).
