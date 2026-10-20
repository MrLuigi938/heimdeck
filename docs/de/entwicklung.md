# Entwicklung

[← Zurück zur Übersicht](../../README.de.md) · [English](../development.md)

Heimdeck ist ein einzelnes Go-Programm mit einer Svelte-Oberfläche, die ins Programm eingebettet wird.

**Voraussetzungen:** Go 1.26+, Node.js 22+

```sh
cd web && npm install && npm run build && cd ..
HEIMDECK_DATA=./data go run .        # http://localhost:3000
```

Oberfläche mit Hot-Reload (leitet die API an Port 3000 weiter):

```sh
cd web && npm run dev                # http://localhost:5173
```

Tests:

```sh
go test ./...
```

Docker-Image bauen (amd64 + arm64 baut GitHub Actions):

```sh
docker build -t heimdeck .
```

## Aufbau

| Ordner | Inhalt |
|---|---|
| `main.go` | Start, Befehle `healthcheck` und `reset-password` |
| `internal/server` | HTTP-API, Überwachung, Benachrichtigungen, Heimdall-Import |
| `internal/integrations` | Eine Datei pro Dienst |
| `internal/store` | SQLite-Datenbank |
| `internal/auth`, `internal/secret` | Anmeldung und Verschlüsselung der Schlüssel |
| `internal/docker` | Docker-Erkennung, Updates und Steuerung |
| `web` | Svelte-Oberfläche (`web/src`), eingebettet über `web/embed.go` |
| `install` | Installationsskripte für Debian/Ubuntu und Proxmox-LXC |

## Eigene Integration

Eine neue Integration ist eine einzelne Datei in `internal/integrations/`:

- `Def()` beschreibt die Felder (Adresse, API-Key, …) und das Icon.
- `Fetch()` liefert ein generisches `Widget` (Zahlen, Liste, Hinweise, Knöpfe). Die Oberfläche braucht dafür keinen eigenen Code.
- Optional: `Search()` für die gemeinsame Suche, `Action()` für Knöpfe und `ImageRequest()` für Cover und Vorschaubilder, die einen API-Key brauchen – die lädt Heimdeck dann selbst, sodass der Key nie im Browser landet.

Am besten eine vorhandene Integration eines ähnlichen Dienstes und ihren Test in `services_test.go` als Vorlage nehmen.

## Releases

Ein Tag wie `v1.2.0` baut und veröffentlicht:

- das Docker-Image `ghcr.io/mrluigi938/heimdeck` mit den Tags `1.2.0` und `1.2`,
- ein GitHub-Release mit Programmen für Linux amd64 und arm64.

Jeder Push auf `main` aktualisiert das Image `latest`.
