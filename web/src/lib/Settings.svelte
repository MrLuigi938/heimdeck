<script>
  import { put, post, get, del } from './api.js';
  import { toast } from './toast.svelte.js';
  import Modal from './Modal.svelte';
  import { detectLocal } from './network.svelte.js';
  import { t, i18n, setLang } from './i18n.svelte.js';
  import { confirmDialog } from './dialog.svelte.js';

  let { settings, boards = [], username, onClose, onSaved, onSwitchBoard } = $props();

  // Editable copy of the dashboards list.
  // svelte-ignore state_referenced_locally
  let boardRows = $state(boards.map((b) => ({ ...b, name: t(b.name) })));
  let newBoard = $state({ name: '', icon: '💼' });

  async function saveBoard(b) {
    try {
      await put(`/boards/${b.id}`, { name: b.name, icon: b.icon, background: b.background });
      toast(t('Gespeichert'));
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
    title: 'Heimdeck', theme: 'auto', accent: '#5b8cff', background: '', searchEngine: 'duckduckgo',
    openInNewTab: 'true', showGreeting: 'true', showUptime: 'true', localHosts: '', uniformTiles: 'false',
    ...$state.snapshot(settings),
  });
  let pw = $state({ current: '', new: '' });
  const accents = ['#5b8cff', '#3ecf8e', '#a78bfa', '#f5b942', '#fb7d4b', '#f2555a', '#22c3d6', '#ec4899'];

  const flag = (key) => s[key] === 'true';
  const setFlag = (key) => (e) => (s[key] = String(e.target.checked));

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

  async function save() {
    try {
      await put('/settings', $state.snapshot(s));
      toast(t('Einstellungen gespeichert'));
      onSaved();
    } catch (e) {
      toast(e.message, 'bad');
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
    const file = e.target.files[0];
    if (!file) return;
    try {
      const res = await post('/import', JSON.parse(await file.text()));
      toast(t('{n} Kachel(n) importiert', { n: res.tiles }));
      onSaved();
    } catch (err) {
      toast(err.message || t('Datei konnte nicht gelesen werden'), 'bad');
    }
  }

  async function logout() {
    await post('/logout');
    location.reload();
  }
</script>

<Modal title={t('Einstellungen')} {onClose}>
  <section>
    <h3>{t('Aussehen')}</h3>
    <div class="grid2">
      <label class="field">
        <span>{t('Sprache')}</span>
        <select value={i18n.lang} onchange={changeLanguage}>
          <option value="de">Deutsch</option>
          <option value="en">English</option>
        </select>
      </label>
      <label class="field">
        <span>{t('Farbschema')}</span>
        <select bind:value={s.theme}>
          <option value="auto">{t('Automatisch (wie System)')}</option>
          <option value="dark">{t('Dunkel')}</option>
          <option value="light">{t('Hell')}</option>
        </select>
      </label>
    </div>
    <label class="field"><span>{t('Titel')}</span><input bind:value={s.title} /></label>
    <div class="field">
      <span>{t('Akzentfarbe')}</span>
      <div class="row wrap">
        {#each accents as c}
          <button class="swatch" class:active={s.accent === c} style="background:{c}" onclick={() => (s.accent = c)} aria-label={c}></button>
        {/each}
        <input type="color" bind:value={s.accent} aria-label={t('Eigene Farbe')} />
      </div>
    </div>
    <label class="field">
      <span>{t('Hintergrundbild (URL, optional)')}</span>
      <input bind:value={s.background} placeholder="https://…/wallpaper.jpg" />
    </label>
    <label class="check"><input type="checkbox" checked={flag('uniformTiles')} onchange={setFlag('uniformTiles')} /> {t('Einheitliche Kachelgrößen (alle Kacheln gleich breit, gleiche Höhe pro Reihe)')}</label>
    <p class="help indent">{t('Gilt für alle Gruppen – außer denen, bei denen du unter „Gruppe bearbeiten“ eine eigene Kachelgröße gewählt hast.')}</p>
    <label class="check"><input type="checkbox" checked={s.showGreeting !== 'false'} onchange={setFlag('showGreeting')} /> {t('Begrüßung, Datum und Uhrzeit anzeigen')}</label>
  </section>

  <section>
    <h3>{t('Verhalten')}</h3>
    <label class="check"><input type="checkbox" checked={s.openInNewTab !== 'false'} onchange={setFlag('openInNewTab')} /> {t('Links in neuem Tab öffnen')}</label>
    <label class="field">
      <span>{t('Websuche')}</span>
      <select bind:value={s.searchEngine}>
        <option value="duckduckgo">DuckDuckGo</option>
        <option value="brave">Brave Search</option>
        <option value="startpage">Startpage</option>
        <option value="google">Google</option>
        <option value="bing">Bing</option>
      </select>
    </label>
    <label class="check"><input type="checkbox" checked={s.showUptime !== 'false'} onchange={setFlag('showUptime')} /> {t('Verfügbarkeits-Übersicht unten anzeigen')}</label>
    <div class="row"><span class="spacer"></span><button class="primary" onclick={save}>{t('Speichern')}</button></div>
  </section>

  <section>
    <h3>{t('Dashboards')}</h3>
    <p class="help">{t('Lege z. B. ein eigenes Dashboard für die Arbeit an. Gewechselt wird oben neben dem Titel; jeder Browser merkt sich das zuletzt benutzte.')} {t('Ohne eigenes Hintergrundbild wird das allgemeine aus „Aussehen“ verwendet.')}</p>
    {#each boardRows as b (b.id)}
      <div class="board-row">
        <input class="emoji" bind:value={b.icon} maxlength="4" aria-label={t('Symbol')} />
        <input bind:value={b.name} aria-label={t('Name')} />
        <input class="board-bg" bind:value={b.background} placeholder={t('Eigenes Hintergrundbild (URL, optional)')} aria-label={t('Hintergrundbild')} />
        <div class="board-actions">
          <button class="small" onclick={() => saveBoard(b)}>{t('Speichern')}</button>
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

  <section>
    <h3>{t('Zuhause oder unterwegs')}</h3>
    <p class="help">
      {t('Hat eine Kachel eine öffentliche Adresse, öffnet Heimdeck sie automatisch, wenn du von unterwegs zugreifst. Als „zuhause“ gilt, wenn du das Dashboard über eine lokale Adresse öffnest (z. B. 192.168.…, localhost, *.local).')}
      {t('Gerade erkannt')}: <b>{detectLocal(s.localHosts) ? '🏠 ' + t('Zuhause') : '🌐 ' + t('Unterwegs')}</b> ({location.hostname})
    </p>
    <label class="field">
      <span>{t('Weitere Adressen, die als „zuhause“ gelten (optional)')}</span>
      <input bind:value={s.localHosts} placeholder={t('z. B. dashboard.home, 10.8.0.1')} />
      <span class="help">{t('Mit Komma trennen. Nützlich, wenn du das Dashboard zuhause über einen eigenen Hostnamen öffnest.')}</span>
    </label>
    <div class="row"><span class="spacer"></span><button class="primary" onclick={save}>{t('Speichern')}</button></div>
  </section>

  <section>
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
  </section>

  <section>
    <h3>{t('Sicherung')}</h3>
    <p class="help">{t('Exportiert Gruppen, Kacheln und Einstellungen – API-Keys werden aus Sicherheitsgründen nicht mitgesichert.')}</p>
    <div class="row">
      <button onclick={exportBackup}>{t('Exportieren')}</button>
      <label class="button">{t('Importieren')}<input type="file" accept="application/json" onchange={importBackup} hidden /></label>
    </div>
  </section>
</Modal>

<style>
  section { display: grid; gap: 12px; padding-bottom: 16px; border-bottom: 1px solid var(--border); }
  section:last-child { border-bottom: 0; padding-bottom: 0; }
  h3 { margin: 4px 0 0; font-size: 14px; }
  .field { display: grid; gap: 6px; font-size: 13px; color: var(--muted); }
  .wrap { flex-wrap: wrap; }
  .swatch { width: 30px; height: 30px; padding: 0; border-radius: 50%; border: 2px solid transparent; }
  .swatch.active { border-color: var(--text); }
  .grid2 { display: grid; grid-template-columns: 1fr 1fr; gap: 8px; }
  .indent { margin: -6px 0 0 26px; }
  .board-row { display: grid; grid-template-columns: 52px minmax(0, 1fr) auto; gap: 6px; align-items: center; }
  .board-row input.emoji { text-align: center; padding-left: 4px; padding-right: 4px; }
  .board-actions { display: flex; gap: 6px; justify-content: flex-end; }
  /* Background URL gets its own line under icon and name. */
  .board-bg { grid-column: 1 / -1; grid-row: 2; font-size: 13px; }
  .board-row:not(form) { margin-bottom: 8px; }
  /* On phones the buttons get their own line below icon and name. */
  @media (max-width: 560px) {
    .board-row { grid-template-columns: 52px minmax(0, 1fr); margin-bottom: 6px; }
    .board-actions { grid-column: 1 / -1; }
    .board-actions button { flex: 1; }
  }
  .button { cursor: pointer; border: 1px solid var(--border-strong); background: var(--surface-2); border-radius: 10px; padding: 8px 14px; }
  .button:hover { border-color: var(--accent); }
  @media (max-width: 520px) { .grid2 { grid-template-columns: 1fr; } }
</style>
