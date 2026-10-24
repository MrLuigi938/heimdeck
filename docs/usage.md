# Usage

[← Back to the README](../README.md) · [Deutsch](de/benutzung.md)

- [Moving from Heimdall](#moving-from-heimdall)
- [Dashboards and groups](#dashboards-and-groups)
- [Home or away](#home-or-away)
- [Search](#search)
- [Notifications](#notifications)
- [Docker labels](#docker-labels)
- [Icons](#icons)
- [Backup](#backup)
- [Forgot your password?](#forgot-your-password)
- [Tips](#tips)

## Moving from Heimdall

1. In Heimdall: Settings → **Export** – this downloads `HeimdallExport.json`.
2. In Heimdeck: click **Import from Heimdall** on the start page (or Settings → Account and backup → Import) and choose the file.

Tags become groups; tiles without a tag go to the group “Heimdall”. Services Heimdeck knows (Sonarr, Radarr, Jellyfin, Nextcloud, AdGuard, Proxmox …) are imported as live tiles – including API key, user and password from Heimdall, with the “Override URL” as the internal address.

## Dashboards and groups

- **Multiple dashboards**, e.g. “Home” and “Work”: switch next to the title at the top. Each dashboard can have its own background image and theme. Every browser remembers the last one used; `?board=<id>` links to a specific one.
- **Groups** sort your tiles. The tile size (automatic, uniform or compact as an icon square) can be set for all groups or per group (edit group → Display). With the display **Free grid** (per group) you place the tiles yourself: in edit mode (✎) a 12-column grid appears under the group, tiles can be dragged anywhere and made wider, narrower, taller or shorter with the bottom-right corner (double-clicking the corner fits the height to the content, the arrow keys resize as well). A tile only one column wide is shown as an icon, so large live tiles and small icons can share a group. Gaps stay where you leave them; a tile dropped onto another pushes that one down. On phones the tiles follow the arrangement in reading order and take the height their content needs: live tiles of 4 or more columns use the whole width, narrower ones half of it, and plain links become icons that fill rows together – nothing is cut off and no tile stays half empty. The arrangement on larger screens is not changed by this. Moving and resizing only works on a larger screen.
- **Hidden groups** are only shown after clicking 👁 (for the current session). **Protected groups** need their own password; if you forget it, your login password unlocks them too, so they can still be deleted or get a new password.
- **Online status**: by default only the local address of a tile is checked. Turn on *Check the public address too* in the tile editor, or for all tiles under *Settings → Network and Docker*.
- **Link-only tiles**: every tile can be a plain link and still shows whether the service is reachable. The status check can be turned off per tile.
- **Choosing live data**: in the tile editor, open **Choose live data** below “Show live data on the tile”. Heimdeck asks the service once and lists every value it delivers plus the list, notes and buttons – untick what you don't need. Values a later version adds show up on their own; for the FRITZ!Box you also choose which sections are loaded at all.

## Themes

*Settings → Appearance → Theme* changes the look with one click, without touching the layout:

- **Classic**: Heimdeck Dark and Light (or automatically like the system), Nord, Mocha, Retro Warm, Vampire and Sand – colour palettes for every taste.
- **Playful**: Terminal (green on black), Neon City (cut corners, neon yellow and cyan), Luna 2001 (every tile a window with a blue title bar), Aero Glass (frosted glass frames), Grim Hunter (dark fantasy with brass frames), Guild Hall (gold frames, progress as experience bars) and Halloween (pumpkins and cobwebs). *Halloween automatically in October* switches to it for the month.
- **Style options** work with every theme: corners (square or very round), tiles (glass or solid – solid is easier on weak tablets), font (system or monospace) and spacing (compact).
- Every theme brings its **accent colour**; pick your own one below the themes, ✦ goes back to the theme's.
- **Per dashboard**: under *Settings → Dashboards* a dashboard can get its own theme, e.g. Nord for “Work”.
- A background image of your own (general or per dashboard) replaces the theme's backdrop.

## Home or away

Each tile can have a second, public address. If you open the dashboard via a local address, Heimdeck uses the local links, otherwise the public ones. You can switch manually with one click at the top right.

“At home” means the dashboard was opened via a private IP (`192.168.…`, `10.…`, `172.16–31.…`, `100.64–127.…` e.g. Tailscale), `localhost`, `*.local`, `*.lan` or a host name without a dot. You can add more host names in the settings.

**Internal address:** if Heimdeck reaches a service differently than your browser (e.g. `http://radarr:7878` in the same Docker network), enter it under “More options” on the tile.

## Search

![Search with results from Jellyfin and Jellyseerr](images/search.webp)
<sub>Demo data</sub>

Press `/` anywhere (or click the search box). One search covers your tiles and every connected service that supports search – media libraries, requests, documents, photos and people, games, audiobooks, comics, files, smart-home devices, network clients, DNS queries and more, with covers and thumbnails. Paste a video URL to send it straight to Tubesy.

*Settings → Search → How results are shown* offers three views:

- **Search window** (default): opens right below the search field when you click it, or in the middle when you press the shortcut further down the page (*Jump up to the search field with the shortcut* scrolls up instead). Filters at the top show only what is there (Tiles, Media, Documents, Smart Home …, with the number of hits); ↑ ↓ select, Enter opens, Tab switches the filter, Esc closes. On the right a preview shows the selected hit with its buttons (e.g. Request, Toggle) and a link into the service. While the field is empty, your recent searches are offered. On phones the window fills the screen and a tap opens the preview from below.
- **Columns**: below the search field, one column per service that found something.
- **List**: the compact list below the search field.

Starting with `!name` searches only the services whose tile name begins with it, e.g. `!jelly severance` or `!paper invoice`.

Pressing Enter without a matching tile searches the web. Choose the search engine in the settings – besides DuckDuckGo, Brave, Startpage, Google, Bing, Ecosia, Qwant and Kagi you can enter your own address (e.g. SearXNG), with `{q}` for the search term.

Which services can be searched is listed in [Supported services](services.md).

## Notifications

Settings → Notifications:

- **Discord**: create a webhook in Discord (Server settings → Integrations → Webhooks) and paste its URL.
- **ntfy**: subscribe to a topic in the ntfy app and enter its address (e.g. `https://ntfy.sh/my-heimdeck` or your own ntfy server), optionally with a token.
- **Telegram**: messages come through a bot of your own:
  1. Open [@BotFather](https://t.me/BotFather) in Telegram (Telegram's official tool for bots) and send it `/newbot`.
  2. Choose a name and a username for the bot (the username ends in `bot`). BotFather answers with a token like `123456789:AAH…` – enter it in Heimdeck as **Telegram bot token**.
  3. Open the new bot (BotFather sends the link `t.me/…`) and tap **Start** or write it any message. For a group: add the bot to the group and write something there.
  4. Click **Find chat ID** in Heimdeck – it asks Telegram who wrote to the bot and fills in the ID (several chats are offered to choose from).
  5. **Save** and try it with **Send test**.
- **Browser**: can be enabled per browser. Works as long as a Heimdeck tab is open, and only via HTTPS or `localhost`.

You get a message when a service hasn't answered for the configured time (default 2 minutes) and when it is back. Only tiles with a status check are monitored.

## Docker labels

Labels on your containers override what Heimdeck detects automatically:

```yaml
labels:
  - heimdeck.name=Movies
  - heimdeck.url=https://radarr.example.com
  - heimdeck.icon=radarr
  - heimdeck.group=Media
  - heimdeck.hide=true      # don't suggest this container
```

## Icons

Use a name from [dashboardicons.com](https://dashboardicons.com) (e.g. `jellyfin`), an image URL or an emoji. Images of any aspect ratio are fitted into the icon area.

## Backup

- **Settings → Account and backup → Export** saves your dashboards, groups and tiles as a file – **without** API keys and passwords. Import it again in the same place.
- For a full backup including keys, back up the data folder (`./data` with Docker, `/var/lib/heimdeck` with the install script). It contains the database and the encryption key – keep it safe.

## Forgot your password?

```sh
# Docker
docker exec heimdeck /heimdeck reset-password <new-password>

# Install script / Proxmox LXC
sudo -u heimdeck HEIMDECK_DATA=/var/lib/heimdeck heimdeck reset-password <new-password>
```

The new password needs at least 8 characters.

## Troubleshooting

Heimdeck always logs errors, warnings and requests that take longer than 5 seconds. If it hangs – the database or its internal state does not respond for 5 seconds – it writes where every part of the program is waiting.

For more detail turn on **Settings → Troubleshooting → Debug mode**: the log then shows every request with its duration, every live data fetch (with the error, if any) and every status check. In the browser all requests also appear in the console (F12). Turn it off again afterwards. If Heimdeck cannot be opened at all, set `HEIMDECK_DEBUG=true` instead (Docker: `environment:`, install script: `/etc/default/heimdeck`) and restart it.

```sh
# Docker
docker logs heimdeck --since 30m

# Install script / Proxmox LXC
sudo journalctl -u heimdeck --since "30 min ago"
```

When reporting a problem, please attach this log – it contains no passwords or API keys.

## Keyboard shortcuts

| Key | Action |
|---|---|
| `/` | Open the search |
| `E` | Edit mode on/off |
| `,` | Open the settings |
| `H` | Show/hide hidden groups |
| `N` | Switch home/away |
| `[` / `]` | Previous / next dashboard |
| `1`–`9` | Open the dashboard with that number |
| `?` | Show all shortcuts |

Change them under **Settings → Keyboard shortcuts**: click a key and press the new one (Delete removes a shortcut, Esc cancels), or turn them off. Shortcuts do nothing while you type in a field or a dialog is open.

## Tips

- **Language**: choose English or German during setup or later in the settings. Live data and error messages are translated too.
- **Behind HTTPS**: set `HEIMDECK_SECURE_COOKIE=true` – see [Installation](installation.md#behind-a-reverse-proxy-https).
- **Uptime overview**: at the bottom of the dashboard you can open the availability of all services for the last 24 hours or 3 days, with outages and response times (can be hidden in the settings).
