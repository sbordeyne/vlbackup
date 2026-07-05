# Helm Chart

The official victoria-logs-single helm chart can be used to deploy `vlbackup` as a sidecar. Here is an example of `values.yaml`

=== "GCS (`gs://`)"

    ```yaml
    server:
      extraArgs:
        partitionManageAuthKey: ...
      extraVolumes:
        - name: google-credentials
          secret:
            secretName: google-credentials
      extraContainers:
        - name: snapshot
          image: ghcr.io/sbordeyne/vlbackup:v3.1.1
          args:
            - --victoria-logs-url=http://127.0.0.1:9428
            - --host=:8080
            - --ops-host=:9090
            - --data-path=/storage
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
            - name: server-volume
              mountPath: /storage
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
    server:
      extraArgs:
        partitionManageAuthKey: ...
      extraContainers:
        - name: snapshot
          image: ghcr.io/sbordeyne/vlbackup:v3.1.1
          args:
            - --victoria-logs-url=http://127.0.0.1:9428
            - --host=:8080
            - --ops-host=:9090
            - --data-path=/storage
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
            - name: server-volume
              mountPath: /storage
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
