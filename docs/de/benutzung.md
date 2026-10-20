# Benutzung

[← Zurück zur Übersicht](../../README.de.md) · [English](../usage.md)

- [Umstieg von Heimdall](#umstieg-von-heimdall)
- [Dashboards und Gruppen](#dashboards-und-gruppen)
- [Zuhause oder unterwegs](#zuhause-oder-unterwegs)
- [Suche](#suche)
- [Benachrichtigungen](#benachrichtigungen)
- [Docker-Labels](#docker-labels)
- [Icons](#icons)
- [Sicherung](#sicherung)
- [Passwort vergessen?](#passwort-vergessen)
- [Tipps](#tipps)

## Umstieg von Heimdall

1. In Heimdall: Einstellungen → **Exportieren** – das lädt `HeimdallExport.json` herunter.
2. In Heimdeck: auf der Startseite **Von Heimdall übernehmen** (oder Einstellungen → Sicherung → Importieren) und die Datei wählen.

Tags werden zu Gruppen, Kacheln ohne Tag landen in der Gruppe „Heimdall“. Dienste, die Heimdeck kennt (Sonarr, Radarr, Jellyfin, Nextcloud, AdGuard, Proxmox …), werden als Live-Kachel übernommen – mit API-Key, Benutzer und Passwort aus Heimdall sowie der „Override URL“ als interne Adresse.

## Dashboards und Gruppen

- **Mehrere Dashboards**, z. B. „Privat“ und „Arbeit“: oben neben dem Titel umschalten. Jedes Dashboard kann ein eigenes Hintergrundbild haben. Jeder Browser merkt sich das zuletzt benutzte, per `?board=<id>` lässt sich eines direkt verlinken.
- **Gruppen** ordnen deine Kacheln. Die Kachelgröße (automatisch, einheitlich oder kompakt als Icon-Quadrat) lässt sich für alle oder pro Gruppe einstellen.
- **Ausgeblendete Gruppen** erscheinen erst nach Klick auf 👁 (für die aktuelle Sitzung). **Geschützte Gruppen** brauchen ein eigenes Passwort.
- **Nur-Link-Kacheln**: Jede Kachel kann ein einfacher Link sein und zeigt trotzdem an, ob der Dienst erreichbar ist. Die Statusprüfung lässt sich pro Kachel abschalten.

## Zuhause oder unterwegs

Jede Kachel kann zusätzlich eine öffentliche Adresse haben. Öffnest du das Dashboard über eine lokale Adresse, werden die lokalen Links verwendet, sonst die öffentlichen. Umschalten geht oben rechts per Klick.

Als „zuhause“ gilt, wenn das Dashboard über eine private IP (`192.168.…`, `10.…`, `172.16–31.…`, `100.64–127.…` z. B. Tailscale), `localhost`, `*.local`, `*.lan` oder einen Namen ohne Punkt geöffnet wird. Weitere Hostnamen kannst du in den Einstellungen eintragen.

**Interne Adresse:** Erreicht Heimdeck einen Dienst anders als dein Browser (z. B. `http://radarr:7878` im selben Docker-Netzwerk), trag das an der Kachel unter „Weitere Optionen“ ein.

## Suche

![Suche mit Treffern aus Jellyfin und Jellyseerr](../images/search.webp)
<sub>Demo-Daten, Oberfläche auf Englisch</sub>

Taste `/` drücken (oder ins Suchfeld klicken). Eine Suche umfasst deine Kacheln und jeden verbundenen Dienst mit Suchfunktion – Medienbibliotheken, Anfragen, Dokumente, Fotos und Personen, Spiele, Hörbücher, Comics, Dateien, Smart-Home-Geräte, Netzwerk-Clients, DNS-Abfragen und mehr, mit Covern und Vorschaubildern. Eine eingefügte Video-URL lässt sich direkt an Tubesy schicken.

Welche Dienste durchsucht werden können, steht unter [Unterstützte Dienste](dienste.md).

## Benachrichtigungen

Einstellungen → Benachrichtigungen:

- **Discord**: In Discord unter Servereinstellungen → Integrationen → Webhooks einen Webhook anlegen und die URL eintragen.
- **ntfy**: Ein Thema in der ntfy-App abonnieren und dessen Adresse eintragen (z. B. `https://ntfy.sh/mein-heimdeck` oder dein eigener ntfy-Server), optional mit Token.
- **Browser**: Pro Browser einschaltbar. Funktioniert, solange ein Heimdeck-Tab offen ist, und nur über HTTPS oder `localhost`.

Gemeldet wird, wenn ein Dienst die eingestellte Zeit (Standard 2 Minuten) nicht antwortet, und wenn er wieder erreichbar ist. Nur Kacheln mit Statusprüfung.

## Docker-Labels

Labels an deinen Containern überschreiben die automatische Erkennung:

```yaml
labels:
  - heimdeck.name=Filme
  - heimdeck.url=https://radarr.example.com
  - heimdeck.icon=radarr
  - heimdeck.group=Medien
  - heimdeck.hide=true      # Container nicht vorschlagen
```

## Icons

Namen aus [dashboardicons.com](https://dashboardicons.com) (z. B. `jellyfin`), eine Bild-URL oder ein Emoji. Bilder in jedem Seitenverhältnis werden ins Icon-Feld eingepasst.

## Sicherung

- **Einstellungen → Exportieren** speichert Dashboards, Gruppen und Kacheln als Datei – **ohne** API-Keys und Passwörter. Wieder einspielen unter Einstellungen → Sicherung.
- Für eine vollständige Sicherung samt Schlüsseln den Datenordner sichern (`./data` bei Docker, `/var/lib/heimdeck` beim Installationsskript). Er enthält die Datenbank und den Verschlüsselungsschlüssel – gut aufbewahren.

## Passwort vergessen?

```sh
# Docker
docker exec heimdeck /heimdeck reset-password <neues-passwort>

# Installationsskript / Proxmox-LXC
sudo -u heimdeck HEIMDECK_DATA=/var/lib/heimdeck heimdeck reset-password <neues-passwort>
```

Das neue Passwort braucht mindestens 8 Zeichen.

## Tipps

- **Sprache**: Deutsch oder Englisch bei der Einrichtung oder später in den Einstellungen wählen. Auch Live-Daten und Fehlermeldungen werden übersetzt.
- **Hinter HTTPS**: `HEIMDECK_SECURE_COOKIE=true` setzen – siehe [Installation](installation.md#hinter-einem-reverse-proxy-https).
- **Verfügbarkeits-Übersicht**: Unten auf dem Dashboard lässt sich die Verfügbarkeit aller Dienste über 24 Stunden oder 3 Tage aufklappen, mit Ausfällen und Antwortzeiten (in den Einstellungen abschaltbar).
