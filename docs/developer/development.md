# Development

## Prerequisites

- Go (see `go.mod` for the exact version; `mise` pins the toolchain).
- [mise](https://mise.jdx.dev/) for tasks and the docs toolchain (optional but recommended).
- Docker — the integration tests spin up real containers via [testcontainers](https://golang.testcontainers.org/).

With mise installed, `mise install` provisions Go, goreleaser, and the docs stack.

## Build

```sh
go build -o vlbackup ./cmd/vlbackup
```

## Test

```sh
# Everything, including container-backed integration tests (needs Docker):
go test ./...

# Fast unit tests only — integration tests skip themselves under -short:
go test ./... -short
```

Integration tests (`pkg/objstore`, `pkg/http_handler`) start fake-gcs-server, MinIO, and VictoriaLogs containers and exercise the real code paths end to end. They call `testing.Short()` and skip when `-short` is set.

## Format & lint

CI enforces both (see `.github/workflows/pr.yaml`):

```sh
gofmt -l .            # must print nothing
golangci-lint run
```

## Docs

The docs site is built with [properdocs](https://github.com/) (a mkdocs fork). mise tasks:

```sh
mise run docs          # live-reload preview on http://127.0.0.1:8000
mise run docs:build    # strict build (fails on broken links)
mise run docs:deploy   # push to the gh-pages branch
```

The nav is generated automatically from the `docs/` directory structure by the awesome-pages plugin — add a Markdown file under `user-guide/`, `developer/`, or `reference/` and it appears in the matching tab. Per-section titles and ordering live in each folder's `.pages` file.

## Release

Releases are cut by [GoReleaser](https://goreleaser.com/) when a `v*` tag is pushed (`.github/workflows/release.yaml`). It builds `linux/amd64` + `linux/arm64` binaries, publishes `tar.gz` archives, and pushes the multi-arch image to `ghcr.io/sbordeyne/vlbackup`.
