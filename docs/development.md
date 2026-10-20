# Development

[← Back to the README](../README.md) · [Deutsch](de/entwicklung.md)

Heimdeck is a single Go program with a Svelte frontend that is embedded into the binary.

**Requirements:** Go 1.26+, Node.js 22+

```sh
cd web && npm install && npm run build && cd ..
HEIMDECK_DATA=./data go run .        # http://localhost:3000
```

Frontend with hot reload (proxies the API to port 3000):

```sh
cd web && npm run dev                # http://localhost:5173
```

Tests:

```sh
go test ./...
```

Build the Docker image (amd64 + arm64 are built by GitHub Actions):

```sh
docker build -t heimdeck .
```

## Project layout

| Folder | Content |
|---|---|
| `main.go` | Start-up, `healthcheck` and `reset-password` commands |
| `internal/server` | HTTP API, monitoring, notifications, Heimdall import |
| `internal/integrations` | One file per service |
| `internal/store` | SQLite database |
| `internal/auth`, `internal/secret` | Login and encryption of keys |
| `internal/docker` | Docker discovery, updates and control |
| `web` | Svelte frontend (`web/src`), embedded via `web/embed.go` |
| `install` | Install scripts for Debian/Ubuntu and Proxmox LXC |

## Adding an integration

A new integration is a single file in `internal/integrations/`:

- `Def()` describes the fields (address, API key, …) and the icon.
- `Fetch()` returns a generic `Widget` (numbers, list, hints, buttons). The frontend needs no extra code.
- Optional: `Search()` for the global search, `Action()` for buttons, and `ImageRequest()` for covers and thumbnails that need an API key – Heimdeck then loads them itself, so the key never reaches the browser.

Have a look at an existing integration of a similar service and its test in `services_test.go`.

## Releases

Pushing a tag like `v1.2.0` builds and publishes:

- the Docker image `ghcr.io/mrluigi938/heimdeck` with the tags `1.2.0` and `1.2`,
- a GitHub release with binaries for Linux amd64 and arm64.

Every push to `main` updates the `latest` image.
