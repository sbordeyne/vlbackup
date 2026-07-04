# Getting Started

## Requirements

- A running [VictoriaLogs](https://docs.victoriametrics.com/victorialogs/) instance recent enough to expose the `/internal/partition/*` endpoints (snapshot create/delete, attach, detach).
- The vlbackup process must be able to **read the VictoriaLogs data volume at the same path** VictoriaLogs uses (`-storageDataPath`, default `/data`). Running it as a sidecar sharing the volume is the intended deployment.
- Credentials for your object-storage backend (see [Object Storage](object-storage.md)).

## Run the container image

Images are published to GitHub Container Registry for `linux/amd64` and `linux/arm64`:

```sh
docker run --rm \
  -p 8080:8080 \
  -v /path/to/victorialogs/data:/data \
  -e VLBACKUP_VICTORIA_LOGS_URL=http://victorialogs:9428 \
  ghcr.io/sbordeyne/vlbackup:latest
```

The server binds to `:8080` by default and prints `Started server on address :8080`.

## Build from source

```sh
git clone https://github.com/sbordeyne/vlbackup.git
cd vlbackup
go build -o vlbackup ./cmd/vlbackup
./vlbackup --victoria-logs-url=http://127.0.0.1:9428
```

## Try the local stack

The repository ships an [`example/compose.yaml`](https://github.com/sbordeyne/vlbackup/blob/main/example/compose.yaml) that wires up a full playground: a [fake-gcs-server](https://github.com/fsouza/fake-gcs-server) emulator, a log generator, a source and a target VictoriaLogs, two vlbackup sidecars, and trigger containers that periodically fire `/snapshot` and `/api/v1/transfer`.

```sh
cd example
docker compose up
```

The `vlbackup` service is pointed at the GCS emulator via `STORAGE_EMULATOR_HOST: fake-gcs:4443`, so snapshots land in the emulator instead of real GCS.

## Take your first snapshot

With vlbackup reachable on `:8080`, trigger a backup of yesterday's partition to your bucket:

```sh
curl -sL -XPOST http://localhost:8080/snapshot \
  -H "Content-Type: application/json" \
  -d '{
    "destination_url": "gs://my-bucket/vlbackup/",
    "partition_prefix": "20260703"
  }'
```

- `partition_prefix` is optional; it defaults to **yesterday** (UTC, `YYYYMMDD`).
- `destination_url` is required and selects the storage backend by scheme.

Each snapshot is uploaded as `<prefix>/<partition>.tar.gz` (e.g. `vlbackup/20260703.tar.gz`). See the [HTTP API](../reference/http-api.md) for the full response contract and the other endpoints.
