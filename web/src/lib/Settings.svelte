<script>
  import { put, post, get, del } from './api.js';
  import { toast } from './toast.svelte.js';
  import Modal from './Modal.svelte';
  import { detectLocal } from './network.svelte.js';
  import { t, i18n, setLang } from './i18n.svelte.js';
  import { confirmDialog } from './dialog.svelte.js';
  import { importFile } from './importFile.js';
  import { browserNotify, setBrowserNotify, notifySupported } from './browserNotify.svelte.js';
  import { ACTIONS, keymap, usableKey, keyLabel } from './shortcuts.js';
  import { THEMES } from './themes.js';

  // section: the area to open first, e.g. "notify".
  let { settings, boards = [], boardId, notify = {}, docker = false, username, debug = false, debugForced = false, section = '', onClose, onSaved, onSwitchBoard } = $props();

  // Editable copy of the dashboards list.
  // svelte-ignore state_referenced_locally
  let boardRows = $state(boards.map((b) => ({ ...b, name: t(b.name) })));
  let newBoard = $state({ name: '', icon: '💼' });

  async function saveBoard(b) {
    try {
      if (!b.name.trim()) return;
      await put(`/boards/${b.id}`, { name: b.name, icon: b.icon, background: b.background, theme: b.theme || '' });
      flash();
      onSaved({ keepOpen: true });
    } catch (e) {
      toast(e.message, 'bad');
    }
  }
  async function addBoard(e) {
    e.preventDefault();
    try {
      const b = await post('/boards', $state.snapshot(newBoard));
      boardRows.push(b);
      newBoard = { name: '', icon: '💼' };
      toast(t('Dashboard „{name}“ angelegt', { name: b.name }));
      onSaved({ keepOpen: true });
    } catch (err) {
      toast(err.message, 'bad');
    }
  }
  async function removeBoard(b) {
    const ok = await confirmDialog(t('Dashboard „{name}“ mit allen Gruppen und Kacheln löschen?', { name: b.name }), { danger: true });
    if (!ok) return;
    try {
      await del(`/boards/${b.id}`);
      boardRows.splice(boardRows.indexOf(b), 1);
      onSaved({ keepOpen: true });
    } catch (e) {
      toast(e.message, 'bad');
    }
  }

  // svelte-ignore state_referenced_locally
  let s = $state({
    title: 'Heimdeck', theme: 'auto', accent: '#5b8cff', background: '', searchEngine: 'duckduckgo', searchUrl: '',
    openInNewTab: 'true', showGreeting: 'true', showUptime: 'true', localHosts: '', uniformTiles: 'false',
    ntfyUrl: '', notifyAfter: '2', notifyRecovery: 'true', notifyExternal: 'true', dockerUpdates: 'false', dockerControl: 'false',
    ...$state.snapshot(settings),
  });
  // svelte-ignore state_referenced_locally
  s.tileLayout ||= settings.uniformTiles === 'true' ? 'uniform' : 'auto';
  let pw = $state({ current: '', new: '' });
  const accents = ['#5b8cff', '#3ecf8e', '#a78bfa', '#f5b942', '#fb7d4b', '#f2555a', '#22c3d6', '#ec4899'];

  const setFlag = (key) => (e) => saveSetting({ [key]: String(e.target.checked) });

  // Areas of the dialog. Phones first show the list, then one area with a back arrow.
  const SECTIONS = [
    { id: 'general', label: 'Allgemein', icon: '⚙️' },
    { id: 'look', label: 'Aussehen', icon: '🎨' },
    { id: 'boards', label: 'Dashboards', icon: '🗂️' },
    { id: 'search', label: 'Suche', icon: '🔍' },
    { id: 'network', label: 'Netzwerk und Docker', icon: '🏠' },
    { id: 'notify', label: 'Benachrichtigungen', icon: '🔔' },
    { id: 'keys', label: 'Tastenkürzel', icon: '⌨️' },
    { id: 'account', label: 'Konto und Sicherung', icon: '🔐' },
    { id: 'debug', label: 'Fehlersuche', icon: '🐞' },
  ];
  const phone = () => matchMedia('(max-width: 640px)').matches;
  // svelte-ignore state_referenced_locally
  let current = $state(section || (phone() ? '' : 'general'));
  let pane;

  // Searching the settings: every area is rendered (inactive ones hidden), so
  // the search can look at their real, translated text.
  let query = $state('');
  let sectionEls = {};
  let matches = $derived.by(() => {
    const q = query.trim().toLowerCase();
    if (!q) return SECTIONS.map((x) => x.id);
    return SECTIONS.filter((x) => (t(x.label) + ' ' + (sectionEls[x.id]?.textContent || '')).toLowerCase().includes(q)).map((x) => x.id);
  });
  $effect(() => {
    // While searching on a larger screen, show the first area that matches.
    if (query.trim() && matches.length && !matches.includes(current) && !phone()) current = matches[0];
  });
  function open(id) {
    current = id;
    pane?.scrollTo(0, 0);
  }

  // The language switches right away and is saved on its own.
  async function changeLanguage(e) {
    const lang = e.target.value;
    setLang(lang);
    try {
      await put('/settings', { language: lang });
      onSaved({ keepOpen: true });
    } catch (err) {
      toast(err.message, 'bad');
    }
  }

  // Keyboard shortcuts: a click on a key waits for the next key press. Saved right away.
  let keys = $state(keymap(settings));
  let shortcutsOn = $state(settings.shortcutsOff !== 'true');
  let recording = $state('');
  async function saveKeys(extra = {}) {
    try {
      await put('/settings', { shortcuts: JSON.stringify($state.snapshot(keys)), shortcutsOff: String(!shortcutsOn), ...extra });
      onSaved({ keepOpen: true });
    } catch (err) {
      toast(err.message, 'bad');
    }
  }
  function recordKey(e) {
    if (!recording) return;
    e.preventDefault();
    e.stopPropagation();
    const id = recording;
    recording = '';
    if (e.key === 'Escape') return;
    if (e.key === 'Backspace' || e.key === 'Delete') {
      keys[id] = '';
    } else if (!usableKey(e.key)) {
      toast(t('Diese Taste geht nicht – bitte eine Buchstaben-, Zeichen- oder Satzzeichen-Taste wählen (Ziffern 1–9 wechseln das Dashboard).'), 'bad');
      return;
    } else {
      const other = ACTIONS.find((a) => a.id !== id && keys[a.id] === e.key);
      if (other) {
        keys[other.id] = ''; // a key does one thing; the other action loses it
        toast(t('„{key}“ war für „{action}“ belegt – dort ist es jetzt leer.', { key: keyLabel(e.key), action: t(other.label) }));
      }
      keys[id] = e.key;
    }
    saveKeys();
  }
  // One shortcut back to its default; another action using that key gives it up.
  function resetKey(a) {
    const other = ACTIONS.find((o) => o.id !== a.id && keys[o.id] === a.key);
    if (other) keys[other.id] = '';
    keys[a.id] = a.key;
    saveKeys();
  }
  function resetKeys() {
    keys = keymap({});
    saveKeys();
  }

  // The debug mode switches right away and is saved on its own, like the language.
  async function toggleDebug(e) {
    try {
      await put('/settings', { debug: String(e.target.checked) });
      toast(e.target.checked ? t('Debug-Modus eingeschaltet') : t('Debug-Modus ausgeschaltet'));
      onSaved({ keepOpen: true });
    } catch (err) {
      e.target.checked = !e.target.checked;
      toast(err.message, 'bad');
    }
  }

  // Every change is saved right away – there are no Save buttons to forget.
  // A short "Saved" at the bottom of the sidebar confirms it.
  let savedFlash = $state(false);
  let flashTimer;
  function flash() {
    savedFlash = true;
    clearTimeout(flashTimer);
    flashTimer = setTimeout(() => (savedFlash = false), 1800);
  }
  async function saveSetting(change) {
    Object.assign(s, change);
    try {
      await put('/settings', change);
      flash();
      onSaved({ keepOpen: true });
    } catch (err) {
      toast(err.message, 'bad');
    }
  }
  // A theme brings its own accent colour; picking one drops a colour chosen before.
  const pickTheme = (id) => saveSetting({ theme: id, accent: '' });
  // For text fields: saved when the field is left or Enter is pressed.
  const saveField = (key) => () => saveSetting({ [key]: (s[key] || '').trim() });

  // Notification channels. The Discord webhook and the ntfy token are secrets:
  // the server only says whether they are set.
  // svelte-ignore state_referenced_locally
  let nf = $state({ discordWebhook: '', ntfyToken: '', telegramToken: '', discordSet: !!notify.discord, ntfyTokenSet: !!notify.ntfyToken, telegramSet: !!notify.telegram });
  let testing = $state(false);

  // Secrets (webhook, tokens) are saved when their field is left; only filled ones are sent.
  async function saveSecrets() {
    const body = {};
    if (nf.discordWebhook) body.discordWebhook = nf.discordWebhook;
    if (nf.ntfyToken) body.ntfyToken = nf.ntfyToken;
    if (nf.telegramToken) body.telegramToken = nf.telegramToken;
    if (!Object.keys(body).length) return;
    try {
      const res = await put('/notify', body);
      nf = { discordWebhook: '', ntfyToken: '', telegramToken: '', discordSet: res.discord, ntfyTokenSet: res.ntfyToken, telegramSet: res.telegram };
      flash();
      onSaved({ keepOpen: true });
    } catch (e) {
      toast(e.message, 'bad');
    }
  }

  async function removeSecret(key) {
    try {
      const res = await put('/notify', { [key]: '' });
      nf.discordSet = res.discord;
      nf.ntfyTokenSet = res.ntfyToken;
      nf.telegramSet = res.telegram;
      onSaved({ keepOpen: true });
    } catch (e) {
      toast(e.message, 'bad');
    }
  }

  // Asks Telegram which chats wrote to the bot and fills in the chat ID.
  let findingChats = $state(false);
  let chatChoices = $state([]);
  async function findChats() {
    findingChats = true;
    try {
      const res = await post('/notify/telegram/chats', { token: nf.telegramToken || '' });
      chatChoices = res.chats;
      if (!res.chats.length) {
        toast(t('Noch keine Nachricht an deinen Bot gefunden. Schreib ihm in Telegram etwas (z. B. „Hallo“) und klick dann nochmal auf „Chat-ID suchen“.'), 'bad');
      } else {
        await saveSetting({ telegramChat: res.chats[0].id });
        toast(res.chats.length === 1
          ? t('Chat-ID von „{name}“ eingetragen.', { name: res.chats[0].name || res.chats[0].id })
          : t('{n} Chats gefunden – bitte unten auswählen.', { n: res.chats.length }));
      }
    } catch (e) {
      toast(e.message, 'bad');
    }
    findingChats = false;
  }

  async function testNotify() {
    testing = true;
    try {
      await saveSecrets();
      const res = await post('/notify/test');
      toast(t('Testnachricht gesendet ({n} Kanal/Kanäle)', { n: res.sent }));
    } catch (e) {
      toast(e.message, 'bad');
    }
    testing = false;
  }

  async function toggleBrowser(e) {
    const ok = await setBrowserNotify(e.target.checked);
    if (!ok) {
      e.target.checked = browserNotify.on;
      toast(t('Der Browser hat Benachrichtigungen nicht erlaubt'), 'bad');
    }
  }

  async function changePassword() {
    try {
      await put('/password', $state.snapshot(pw));
      pw = { current: '', new: '' };
      toast(t('Passwort geändert'));
    } catch (e) {
      toast(e.message, 'bad');
    }
  }

  async function exportBackup() {
    const data = await get('/export');
    const blob = new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' });
    const a = document.createElement('a');
    a.href = URL.createObjectURL(blob);
    a.download = 'heimdeck-backup.json';
    a.click();
    URL.revokeObjectURL(a.href);
  }

  async function importBackup(e) {
    if (await importFile(e.target.files[0], boardId)) onSaved();
    e.target.value = '';
  }

  async function logout() {
    await post('/logout');
    location.reload();
  }
</script>

<!-- Capture phase: a key being recorded never reaches the dialog's own Esc handling. -->
<svelte:window onkeydowncapture={recordKey} />

<Modal title={t('Einstellungen')} {onClose} size="settings">
  <div class="settings" class:list={!current}>
    <nav class="side" aria-label={t('Bereiche')}>
      <input class="find" type="search" bind:value={query} placeholder={t('Einstellung suchen …')} aria-label={t('Einstellung suchen …')} />
      {#each SECTIONS.filter((x) => matches.includes(x.id)) as x (x.id)}
        <button class="nav" class:on={current === x.id} onclick={() => open(x.id)} aria-current={current === x.id ? 'page' : undefined}>
          <span class="nav-icon" aria-hidden="true">{x.icon}</span><span class="nav-label">{t(x.label)}</span><span class="chev" aria-hidden="true">›</span>
        </button>
      {/each}
      {#if !matches.length}<p class="help nothing">{t('Nichts gefunden.')}</p>{/if}
      <p class="saved" class:show={savedFlash} aria-live="polite">{savedFlash ? '✓ ' + t('Gespeichert') : t('Änderungen werden sofort gespeichert')}</p>
    </nav>

    <div class="pane" bind:this={pane}>
      {#if current}
        <button class="back" onclick={() => (current = '')}>‹ {t('Einstellungen')}</button>
      {/if}

      <section hidden={current !== 'general'} bind:this={sectionEls.general}>
        <h3>{t('Allgemein')}</h3>
        <div class="grid2">
          <label class="field">
            <span>{t('Sprache')}</span>
            <select value={i18n.lang} onchange={changeLanguage}>
              <option value="de">Deutsch</option>
              <option value="en">English</option>
            </select>
          </label>
          <label class="field"><span>{t('Titel')}</span><input bind:value={s.title} onchange={saveField('title')} /></label>
        </div>
        <label class="check"><input type="checkbox" checked={s.showGreeting !== 'false'} onchange={setFlag('showGreeting')} /> {t('Begrüßung, Datum und Uhrzeit anzeigen')}</label>
        <label class="check"><input type="checkbox" checked={s.openInNewTab !== 'false'} onchange={setFlag('openInNewTab')} /> {t('Links in neuem Tab öffnen')}</label>
        <label class="check"><input type="checkbox" checked={s.showUptime !== 'false'} onchange={setFlag('showUptime')} /> {t('Verfügbarkeits-Übersicht unten anzeigen')}</label>
        <label class="check"><input type="checkbox" checked={s.updateCheck !== 'false'} onchange={setFlag('updateCheck')} /> {t('Auf neue Heimdeck-Versionen hinweisen')}</label>
        <p class="help indent">{t('Der Server fragt zweimal am Tag bei GitHub nach der neuesten Version und zeigt sie unten in der Fußzeile an. Es wird nichts automatisch installiert.')}</p>
      </section>

      <section hidden={current !== 'look'} bind:this={sectionEls.look}>
        <h3>{t('Aussehen')}</h3>
        <div class="field">
          <span>{t('Theme')}</span>
          {#each [['classic', 'Klassisch'], ['fun', 'Verspielt']] as [group, label]}
            <div class="theme-group">{t(label)}</div>
            <div class="themes">
              {#each THEMES.filter((th) => th.group === group) as th (th.id)}
                {@const [bg, surface, accent, text] = th.preview}
                <button class="theme" class:active={(s.theme || 'auto') === th.id} onclick={() => pickTheme(th.id)} aria-pressed={(s.theme || 'auto') === th.id}>
                  <span class="swatch-preview" style="background:{th.id === 'auto' ? `linear-gradient(135deg, ${bg} 50%, ${surface} 50%)` : bg}">
                    <span class="mini" style="background:{surface};color:{text}"><span class="mini-line" style="background:{text}"></span><span class="mini-dot" style="background:{accent}"></span></span>
                  </span>
                  <span class="theme-name">{t(th.name)}</span>
                </button>
              {/each}
            </div>
          {/each}
          <label class="check"><input type="checkbox" checked={s.seasonal === 'true'} onchange={setFlag('seasonal')} /> {t('Im Oktober automatisch Halloween')}</label>
        </div>
        <div class="grid4">
          <label class="field">
            <span>{t('Ecken')}</span>
            <select value={s.corners || ''} onchange={(e) => saveSetting({ corners: e.target.value })}>
              <option value="">{t('Wie das Theme')}</option>
              <option value="square">{t('Eckig')}</option>
              <option value="round">{t('Sehr rund')}</option>
            </select>
          </label>
          <label class="field">
            <span>{t('Kacheln')}</span>
            <select value={s.tileStyle || ''} onchange={(e) => saveSetting({ tileStyle: e.target.value })}>
              <option value="">{t('Glas (durchscheinend)')}</option>
              <option value="solid">{t('Massiv')}</option>
            </select>
          </label>
          <label class="field">
            <span>{t('Schrift')}</span>
            <select value={s.font || ''} onchange={(e) => saveSetting({ font: e.target.value })}>
              <option value="">{t('Wie das Theme')}</option>
              <option value="system">{t('System')}</option>
              <option value="mono">{t('Monospace')}</option>
            </select>
          </label>
          <label class="field">
            <span>{t('Abstände')}</span>
            <select value={s.density || ''} onchange={(e) => saveSetting({ density: e.target.value })}>
              <option value="">{t('Normal')}</option>
              <option value="compact">{t('Kompakt')}</option>
            </select>
          </label>
        </div>
        <div class="field">
          <span>{t('Akzentfarbe')}</span>
          <div class="row wrap">
            <button class="swatch theme-accent" class:active={!s.accent} onclick={() => saveSetting({ accent: '' })} title={t('Farbe des Themes')} aria-label={t('Farbe des Themes')}>✦</button>
            {#each accents as c}
              <button class="swatch" class:active={s.accent === c} style="background:{c}" onclick={() => saveSetting({ accent: c })} aria-label={c}></button>
            {/each}
            <input type="color" value={s.accent || '#5b8cff'} onchange={(e) => saveSetting({ accent: e.target.value })} aria-label={t('Eigene Farbe')} />
          </div>
        </div>
        <label class="field">
          <span>{t('Hintergrundbild (URL, optional)')}</span>
          <input bind:value={s.background} onchange={saveField('background')} placeholder="https://…/wallpaper.jpg" />
        </label>
        <label class="field">
          <span>{t('Kachelgröße')}</span>
          <select bind:value={s.tileLayout} onchange={() => saveSetting({ tileLayout: s.tileLayout })}>
            <option value="auto">{t('Automatisch – Kacheln mit Live-Daten breiter')}</option>
            <option value="uniform">{t('Einheitlich – alle Kacheln gleich groß')}</option>
            <option value="icons">{t('Kompakt – Icon mit Namen darunter')}</option>
            <option value="icons-hover">{t('Kompakt – nur Icon, Name beim Darüberfahren')}</option>
          </select>
          <span class="help">{t('Gilt für alle Gruppen – außer denen, bei denen du unter „Gruppe bearbeiten“ eine eigene Kachelgröße gewählt hast.')} {t('Kompakte Kacheln zeigen keine Live-Daten, nur Icon und Status.')}</span>
        </label>
      </section>

      <section hidden={current !== 'boards'} bind:this={sectionEls.boards}>
        <h3>{t('Dashboards')}</h3>
        <p class="help">{t('Lege z. B. ein eigenes Dashboard für die Arbeit an. Gewechselt wird oben neben dem Titel; jeder Browser merkt sich das zuletzt benutzte.')} {t('Ohne eigenes Hintergrundbild wird das allgemeine aus „Aussehen“ verwendet.')}</p>
        {#each boardRows as b (b.id)}
          <div class="board-row">
            <input class="emoji" bind:value={b.icon} onchange={() => saveBoard(b)} maxlength="4" aria-label={t('Symbol')} />
            <input bind:value={b.name} onchange={() => saveBoard(b)} aria-label={t('Name')} />
            <input class="board-bg" bind:value={b.background} onchange={() => saveBoard(b)} placeholder={t('Eigenes Hintergrundbild (URL, optional)')} aria-label={t('Hintergrundbild')} />
            <select class="board-theme" bind:value={b.theme} onchange={() => saveBoard(b)} aria-label={t('Theme')}>
              <option value="">{t('Allgemeines Theme')}</option>
              {#each THEMES.filter((th) => th.id !== 'auto') as th}<option value={th.id}>{t(th.name)}</option>{/each}
            </select>
            <div class="board-actions">
              <button class="small" onclick={() => { onSwitchBoard(b.id); onClose(); }}>{t('Öffnen')}</button>
              {#if boardRows.length > 1}<button class="small danger" onclick={() => removeBoard(b)}>{t('Löschen')}</button>{/if}
            </div>
          </div>
        {/each}
        <form class="board-row" onsubmit={addBoard}>
          <input class="emoji" bind:value={newBoard.icon} maxlength="4" aria-label={t('Symbol')} />
          <input bind:value={newBoard.name} placeholder={t('z. B. Arbeit')} required />
          <div class="board-actions"><button class="small primary">+ {t('Dashboard anlegen')}</button></div>
        </form>
      </section>

      <section hidden={current !== 'search'} bind:this={sectionEls.search}>
        <h3>{t('Suche')}</h3>
        <label class="field">
          <span>{t('Websuche')}</span>
          <select bind:value={s.searchEngine} onchange={() => saveSetting({ searchEngine: s.searchEngine })}>
            <option value="duckduckgo">DuckDuckGo</option>
            <option value="brave">Brave Search</option>
            <option value="startpage">Startpage</option>
            <option value="google">Google</option>
            <option value="bing">Bing</option>
            <option value="ecosia">Ecosia</option>
            <option value="qwant">Qwant</option>
            <option value="kagi">Kagi</option>
            <option value="custom">{t('Benutzerdefiniert …')}</option>
          </select>
        </label>
        {#if s.searchEngine === 'custom'}
          <label class="field">
            <span>{t('Adresse der Suchmaschine')}</span>
            <input bind:value={s.searchUrl} onchange={saveField('searchUrl')} placeholder="https://searx.example.com/search?q={'{q}'}" inputmode="url" />
            <span class="help">{t('{q} wird durch den Suchbegriff ersetzt, z. B. für deine eigene SearXNG-Instanz.')}</span>
          </label>
        {/if}
        <p class="help">{t('Die Suche oben findet deine Kacheln sofort und fragt im Hintergrund alle Dienste, die suchen können (z. B. Jellyfin, Paperless). Enter öffnet den ersten Treffer oder sucht im Web.')}</p>
      </section>

      <section hidden={current !== 'network'} bind:this={sectionEls.network}>
        <h3>{t('Zuhause oder unterwegs')}</h3>
        <p class="help">
          {t('Hat eine Kachel eine öffentliche Adresse, öffnet Heimdeck sie automatisch, wenn du von unterwegs zugreifst. Als „zuhause“ gilt, wenn du das Dashboard über eine lokale Adresse öffnest (z. B. 192.168.…, localhost, *.local).')}
          {t('Gerade erkannt')}: <b>{detectLocal(s.localHosts) ? '🏠 ' + t('Zuhause') : '🌐 ' + t('Unterwegs')}</b> ({location.hostname})
        </p>
        <label class="field">
          <span>{t('Weitere Adressen, die als „zuhause“ gelten (optional)')}</span>
          <input bind:value={s.localHosts} onchange={saveField('localHosts')} placeholder={t('z. B. dashboard.home, 10.8.0.1')} />
          <span class="help">{t('Mit Komma trennen. Nützlich, wenn du das Dashboard zuhause über einen eigenen Hostnamen öffnest.')}</span>
        </label>
        <h3>Docker</h3>
        {#if !docker}<p class="help">{t('Kein Docker-Socket eingebunden – siehe Anleitung in der docker-compose.yml')}</p>{/if}
        <label class="check"><input type="checkbox" checked={s.dockerUpdates === 'true'} onchange={setFlag('dockerUpdates')} disabled={!docker} /> {t('Nach Container-Updates suchen')}</label>
        <p class="help indent">{t('Vergleicht alle 6 Stunden die Images deiner Container mit der Registry (Docker Hub, ghcr.io …). Es wird nichts heruntergeladen oder aktualisiert.')}</p>
        <label class="check"><input type="checkbox" checked={s.dockerControl === 'true'} onchange={setFlag('dockerControl')} disabled={!docker} /> {t('Container starten, stoppen und neu starten erlauben')}</label>
        <p class="help indent">{t('Knöpfe auf der Docker-Kachel und in der Suche. Jeder, der im Dashboard angemeldet ist, kann dann deine Container steuern.')}</p>
      </section>

      <section hidden={current !== 'notify'} bind:this={sectionEls.notify}>
        <h3>{t('Benachrichtigungen')}</h3>
        <p class="help">{t('Heimdeck meldet sich, wenn ein Dienst ausfällt (nur Kacheln mit Online-Prüfung) und wenn er wieder erreichbar ist.')}</p>
        <label class="field">
          <span>Discord-Webhook</span>
          <div class="secret-row">
            <input type="password" bind:value={nf.discordWebhook} onchange={saveSecrets} autocomplete="off" placeholder={nf.discordSet ? '•••••••• ' + t('gespeichert – leer lassen zum Beibehalten') : 'https://discord.com/api/webhooks/…'} />
            {#if nf.discordSet}<button class="small" onclick={() => removeSecret('discordWebhook')}>{t('Entfernen')}</button>{/if}
          </div>
          <span class="help">{t('In Discord: Servereinstellungen → Integrationen → Webhooks → Neuer Webhook → Webhook-URL kopieren.')}</span>
        </label>
        <div class="grid2">
          <label class="field">
            <span>{t('ntfy-Thema (Adresse)')}</span>
            <input bind:value={s.ntfyUrl} onchange={saveField('ntfyUrl')} placeholder="https://ntfy.sh/mein-heimdeck" />
          </label>
          <label class="field">
            <span>{t('ntfy-Token (optional)')}</span>
            <div class="secret-row">
              <input type="password" bind:value={nf.ntfyToken} onchange={saveSecrets} autocomplete="off" placeholder={nf.ntfyTokenSet ? '•••••••• ' + t('gespeichert') : 'tk_… ' + t('oder Benutzer:Passwort')} />
              {#if nf.ntfyTokenSet}<button class="small" onclick={() => removeSecret('ntfyToken')}>{t('Entfernen')}</button>{/if}
            </div>
          </label>
        </div>
        <span class="help">{t('ntfy: App installieren, ein Thema abonnieren und dessen Adresse hier eintragen. Eigener ntfy-Server geht genauso.')}</span>
        <details class="howto">
          <summary>{t('So richtest du Telegram ein')}</summary>
          <ol>
            <li>{t('Öffne in Telegram den Kontakt @BotFather (das offizielle Werkzeug von Telegram für Bots) und schick ihm /newbot.')}</li>
            <li>{t('Gib einen Namen und einen Benutzernamen für deinen Bot ein (der Benutzername muss auf „bot“ enden). BotFather antwortet mit einem Token wie 123456789:AAH… – den hier bei „Bot-Token“ eintragen.')}</li>
            <li>{t('Öffne deinen neuen Bot (BotFather schickt den Link mit, t.me/…) und tippe auf „Starten“ oder schreib ihm irgendeine Nachricht. Für eine Gruppe: den Bot in die Gruppe aufnehmen und dort etwas schreiben.')}</li>
            <li>{t('Klick unten auf „Chat-ID suchen“ – Heimdeck fragt Telegram, wer deinem Bot geschrieben hat, und trägt die ID ein.')}</li>
            <li>{t('Mit „Test senden“ ausprobieren.')}</li>
          </ol>
        </details>
        <div class="grid2">
          <label class="field">
            <span>{t('Telegram-Bot-Token')}</span>
            <div class="secret-row">
              <input type="password" bind:value={nf.telegramToken} onchange={saveSecrets} autocomplete="off" placeholder={nf.telegramSet ? '•••••••• ' + t('gespeichert') : '123456789:AAH…'} />
              {#if nf.telegramSet}<button class="small" onclick={() => removeSecret('telegramToken')}>{t('Entfernen')}</button>{/if}
            </div>
          </label>
          <label class="field">
            <span>{t('Telegram-Chat-ID')}</span>
            <div class="secret-row">
              <input bind:value={s.telegramChat} onchange={saveField('telegramChat')} placeholder="123456789 / -100…" inputmode="numeric" />
              <button class="small" onclick={findChats} disabled={findingChats || (!nf.telegramToken && !nf.telegramSet)}>{t('Chat-ID suchen')}</button>
            </div>
          </label>
        </div>
        {#if chatChoices.length > 1}
          <div class="chat-choices">
            <span class="help">{t('Mehrere Chats gefunden – welcher soll die Meldungen bekommen?')}</span>
            {#each chatChoices as c (c.id)}
              <button class="small" class:primary={s.telegramChat === c.id} onclick={() => saveSetting({ telegramChat: c.id })}>
                {c.type === 'private' ? '👤' : '👥'} {c.name || c.id}
              </button>
            {/each}
          </div>
        {/if}
        <label class="field">
          <span>{t('Melden, wenn ein Dienst so lange nicht antwortet')}</span>
          <select bind:value={s.notifyAfter} onchange={() => saveSetting({ notifyAfter: s.notifyAfter })}>
            <option value="1">1 {t('Minute')}</option>
            <option value="2">2 {t('Minuten')}</option>
            <option value="5">5 {t('Minuten')}</option>
            <option value="10">10 {t('Minuten')}</option>
            <option value="30">30 {t('Minuten')}</option>
          </select>
        </label>
        <label class="check"><input type="checkbox" checked={s.notifyRecovery !== 'false'} onchange={setFlag('notifyRecovery')} /> {t('Melden, wenn der Dienst wieder erreichbar ist')}</label>
        <label class="check"><input type="checkbox" checked={s.notifyExternal !== 'false'} onchange={setFlag('notifyExternal')} /> {t('Auch Ausfälle der öffentlichen Adressen melden')}</label>
        <label class="check">
          <input type="checkbox" checked={browserNotify.on} onchange={toggleBrowser} disabled={!notifySupported()} />
          {t('Browser-Benachrichtigungen in diesem Browser')}
        </label>
        <p class="help indent">
          {notifySupported()
            ? t('Funktioniert, solange ein Heimdeck-Tab offen ist – auch im Hintergrund.')
            : t('Nur über HTTPS (z. B. hinter deinem Reverse Proxy) oder localhost möglich – Browser erlauben Benachrichtigungen sonst nicht.')}
        </p>
        <div class="row"><span class="spacer"></span><button onclick={testNotify} disabled={testing}>{t('Test senden')}</button></div>
      </section>

      <section hidden={current !== 'keys'} bind:this={sectionEls.keys}>
        <h3>{t('Tastenkürzel')}</h3>
        <label class="check"><input type="checkbox" bind:checked={shortcutsOn} onchange={() => saveKeys()} /> {t('Tastenkürzel verwenden')}</label>
        {#if shortcutsOn}
          <p class="help">{t('Auf eine Taste klicken und die neue drücken. Entf löscht ein Kürzel, Esc bricht ab. Die Ziffern 1–9 öffnen immer das Dashboard mit dieser Nummer.')}</p>
          <div class="keys">
            {#each ACTIONS as a (a.id)}
              <span>{t(a.label)}</span>
              <button class="small key" class:primary={recording === a.id} onclick={() => (recording = recording === a.id ? '' : a.id)}>
                {recording === a.id ? t('Taste drücken …') : keyLabel(keys[a.id])}
              </button>
              {#if keys[a.id] !== a.key}
                <button class="small ghost" onclick={() => resetKey(a)} title={t('Zurücksetzen auf {key}', { key: keyLabel(a.key) })} aria-label={t('Zurücksetzen auf {key}', { key: keyLabel(a.key) })}>↺</button>
              {:else}
                <span></span>
              {/if}
            {/each}
          </div>
          <div class="row"><span class="spacer"></span><button class="small" onclick={resetKeys}>{t('Standard wiederherstellen')}</button></div>
        {/if}
      </section>

      <section hidden={current !== 'account'} bind:this={sectionEls.account}>
        <h3>{t('Zugang')} ({username})</h3>
        <div class="grid2">
          <input type="password" placeholder={t('Aktuelles Passwort')} bind:value={pw.current} autocomplete="current-password" />
          <input type="password" placeholder={t('Neues Passwort')} bind:value={pw.new} autocomplete="new-password" />
        </div>
        <div class="row">
          <button onclick={logout}>{t('Abmelden')}</button>
          <span class="spacer"></span>
          <button disabled={!pw.current || pw.new.length < 8} onclick={changePassword}>{t('Passwort ändern')}</button>
        </div>
        <h3>{t('Sicherung')}</h3>
        <p class="help">{t('Exportiert Gruppen, Kacheln und Einstellungen – API-Keys werden aus Sicherheitsgründen nicht mitgesichert.')}</p>
        <p class="help">{t('Importieren nimmt auch einen Heimdall-Export (Heimdall → Einstellungen → Exportieren). Die Kacheln landen im geöffneten Dashboard, bekannte Dienste werden samt API-Key als Live-Kachel übernommen.')}</p>
        <div class="row">
          <button onclick={exportBackup}>{t('Exportieren')}</button>
          <label class="button">{t('Importieren')}<input type="file" accept="application/json" onchange={importBackup} hidden /></label>
        </div>
      </section>

      <section hidden={current !== 'debug'} bind:this={sectionEls.debug}>
        <h3>{t('Fehlersuche')}</h3>
        <label class="check"><input type="checkbox" checked={debug} onchange={toggleDebug} disabled={debugForced} /> {t('Debug-Modus')}</label>
        <p class="help indent">{t('Schreibt ins Protokoll, was Heimdeck gerade tut: jede Anfrage mit ihrer Dauer, das Abholen der Live-Daten und die Statusprüfungen. Im Browser erscheinen die Anfragen zusätzlich in der Konsole (F12). Nur zur Fehlersuche einschalten.')}</p>
        {#if debugForced}<p class="help indent">{t('Eingeschaltet über HEIMDECK_DEBUG=true – lässt sich hier nicht ausschalten.')}</p>{/if}
        <p class="help indent">{t('Protokoll ansehen')}: <code>docker logs heimdeck</code> · {t('Installationsskript und Proxmox')}: <code>journalctl -u heimdeck</code></p>
      </section>
    </div>
  </div>
</Modal>

<style>
  .secret-row { display: flex; gap: 8px; }
  .secret-row input { flex: 1; min-width: 0; }
  /* Sidebar with the areas on the left, the open area on the right; both scroll on their own. */
  .settings { display: grid; grid-template-columns: 220px minmax(0, 1fr); height: 100%; min-height: 0; }
  .side { border-right: 1px solid var(--border); padding: 4px 10px 14px 16px; display: flex; flex-direction: column; gap: 2px; overflow-y: auto; min-height: 0; }
  .find { margin: 2px 0 8px; padding: 7px 10px; font-size: 13px; }
  .nav { display: flex; align-items: center; gap: 10px; width: 100%; border: 0; background: none; padding: 8px 10px; text-align: left; border-radius: var(--r-sm); font-size: 14px; }
  .nav:hover { background: var(--surface-2); }
  .nav.on { background: color-mix(in srgb, var(--accent) 16%, transparent); color: var(--accent); font-weight: 500; }
  .nav-icon { width: 20px; text-align: center; flex: none; }
  .nav-label { flex: 1; min-width: 0; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .chev { display: none; color: var(--muted); font-size: 18px; }
  .nothing { padding: 4px 10px; }
  .saved { margin: auto 0 0; padding: 12px 4px 0; font-size: 12px; color: var(--muted); transition: color .2s; }
  .saved.show { color: var(--ok); }
  .pane { overflow-y: auto; overscroll-behavior: contain; padding: 4px 22px 22px; min-height: 0; }
  section { display: grid; gap: 12px; align-content: start; }
  section[hidden] { display: none; }
  h3 { margin: 4px 0 0; font-size: 16px; }
  section h3 + h3, section > h3:not(:first-child) { margin-top: 12px; padding-top: 14px; border-top: 1px solid var(--border); font-size: 14px; }
  .back { display: none; }
  /* Phones: the list of areas first; an area opens full-width with a back arrow. */
  @media (max-width: 640px) {
    .settings { grid-template-columns: minmax(0, 1fr); }
    .settings.list .pane, .settings:not(.list) .side { display: none; }
    .side { border-right: 0; padding: 0 16px 14px; }
    .nav { padding: 13px 6px; border-radius: 0; border-bottom: 1px solid var(--border); }
    .nav.on { background: none; color: inherit; font-weight: 400; }
    .chev { display: inline; }
    .pane { padding: 0 16px 18px; }
    .back { display: inline-flex; justify-self: start; border: 0; background: none; padding: 2px 0 10px; color: var(--accent); font-size: 14px; }
  }
  .field { display: grid; gap: 6px; font-size: 13px; color: var(--muted); }
  .wrap { flex-wrap: wrap; }
  .swatch { width: 30px; height: 30px; padding: 0; border-radius: 50%; border: 2px solid transparent; }
  .swatch.active { border-color: var(--text); }
  .grid2 { display: grid; grid-template-columns: 1fr 1fr; gap: 8px; }
  .indent { margin: -6px 0 0 26px; }
  .keys { display: grid; grid-template-columns: 1fr auto 32px; gap: 6px 8px; align-items: center; font-size: 14px; }
  .key { min-width: 64px; justify-content: center; font-family: ui-monospace, monospace; font-weight: 600; }
  /* Same look as the setup guides in the tile editor. */
  .howto { background: var(--surface-2); border-radius: var(--r-md); padding: 10px 14px; font-size: 13.5px; }
  .howto summary { cursor: pointer; font-weight: 600; }
  .howto ol { margin: 8px 0 2px; padding-left: 20px; display: grid; gap: 6px; color: var(--muted); }
  .chat-choices { display: flex; flex-wrap: wrap; gap: 6px; align-items: center; }
  .chat-choices .help { flex-basis: 100%; }
  .board-row { display: grid; grid-template-columns: 52px minmax(0, 1fr) auto; gap: 6px; align-items: center; }
  .board-row input.emoji { text-align: center; padding-left: 4px; padding-right: 4px; }
  .board-actions { display: flex; gap: 6px; justify-content: flex-end; }
  /* Background URL gets its own line under icon and name. */
  .board-bg { grid-column: 1 / -1; grid-row: 2; font-size: 13px; }
  .board-theme { grid-column: 1 / -1; grid-row: 3; font-size: 13px; }
  .board-row:not(form) { margin-bottom: 8px; }
  /* Theme cards: a tiny tile in the theme's colours. */
  .theme-group { font-size: 11.5px; text-transform: uppercase; letter-spacing: .06em; color: var(--muted); margin-top: 6px; }
  .themes { display: grid; grid-template-columns: repeat(auto-fill, minmax(104px, 1fr)); gap: 8px; }
  .theme { display: grid; gap: 6px; padding: 6px; text-align: left; border-radius: var(--r-md); background: var(--surface-2); }
  .theme.active { border-color: var(--accent); box-shadow: 0 0 0 2px color-mix(in srgb, var(--accent) 35%, transparent); }
  .swatch-preview { display: grid; place-items: center; height: 48px; border-radius: var(--r-xs); padding: 8px; }
  .mini { width: 100%; height: 100%; border-radius: 4px; display: flex; align-items: center; gap: 6px; padding: 0 6px; }
  .mini-line { flex: 1; height: 4px; border-radius: 2px; opacity: .8; }
  .mini-dot { width: 10px; height: 10px; border-radius: 50%; flex: none; }
  .theme-name { font-size: 12.5px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .theme-accent { background: var(--surface-2); display: inline-grid; place-items: center; font-size: 13px; }
  .grid4 { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 8px; }
  /* On phones the buttons get their own line below icon and name. */
  @media (max-width: 560px) {
    .board-row { grid-template-columns: 52px minmax(0, 1fr); margin-bottom: 6px; }
    .board-actions { grid-column: 1 / -1; }
    .board-actions button { flex: 1; }
  }
  @media (max-width: 520px) { .grid2 { grid-template-columns: 1fr; } }
</style>
