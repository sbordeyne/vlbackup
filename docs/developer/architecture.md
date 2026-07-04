# Architecture

VLBackup is a single Go binary (`cmd/vlbackup`) that serves a small HTTP API and talks to VictoriaLogs and object storage. It has two independent code paths — object-storage backup and peer-to-peer partition transfer — that share very little.

## Package layout

| Package | Responsibility |
| --- | --- |
| `cmd/vlbackup` | `main`: parses args, builds the Prometheus registry, wires the go-chi router. |
| `pkg/cli` | Flag/env parsing (`Args`). |
| `pkg/http_handler` | HTTP handlers: `/snapshot`, `/api/v1/transfer*`, health, bearer auth. |
| `pkg/objstore` | Swappable object-storage layer: `Repository` interface, scheme registry, `gs://` and `s3://` backends. |
| `pkg/transfer` | Peer client, `tar.gz` streaming (`StreamDir`/`ExtractDir`), day-range logic. |
| `pkg/victoriametrics` | Client for VictoriaLogs `/internal/partition/*` endpoints. |
| `pkg/metrics` | Prometheus metric definitions. |

## Request wiring

Handlers are built by factories that close over config and metrics — `TriggerHandlerFactory(args, metrics)`, `TransferHandlerFactory(args, metrics)`, etc. `main` mounts them on a go-chi router; the transfer target endpoints sit behind a `BearerAuth` middleware keyed on `TransferAuthKey`.

## Object-storage layer

`pkg/objstore` decouples the `/snapshot` handler from any specific SDK. The handler never imports a cloud SDK — it calls `objstore.Open(ctx, destinationURL)`, which parses the URL, looks up a backend by scheme, and returns a bucket-scoped `Repository` plus the key prefix.

```go
type Repository interface {
    Upload(ctx context.Context, key string, r io.Reader) error
    Download(ctx context.Context, key string) (io.ReadCloser, error)
    List(ctx context.Context, prefix string) iter.Seq2[ObjectInfo, error]
    Delete(ctx context.Context, key string) error
    Close() error
}
```

Snapshots are streamed straight to storage: `transfer.StreamDir` writes a `tar.gz` into an `io.Pipe`, whose read end is handed to `Repository.Upload` — nothing is staged on local disk.

## Extending storage backends

A backend is one file in `pkg/objstore` that implements `Repository` and registers itself for a URL scheme from an `init()` function:

```go
func init() {
    objstore.Register("azblob", newAzureRepository)
}

func newAzureRepository(ctx context.Context, u *url.URL) (objstore.Repository, error) {
    // u.Host is the bucket/container; read credentials from the environment.
    // ...
}
```

That is all that is required — the `/snapshot` handler dispatches on the destination URL scheme automatically via `objstore.Open`, so no handler or config changes are needed. Follow the existing `gcs.go` / `s3.go` for reference (error mapping to `objstore.ErrNotFound`, streaming upload, prefix handling).

## Partition transfer

The transfer path (`pkg/transfer` + the `TransferHandlerFactory` on the source, `TransferReceiveHandlerFactory`/`TransferAttachHandlerFactory` on the target) streams sealed partitions between two vlbackup sidecars over HTTP. See [Partition Transfer](../user-guide/partition-transfer.md) for the runtime flow and deployment requirements.
