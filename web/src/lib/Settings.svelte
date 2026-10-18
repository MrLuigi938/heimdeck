<script>
  import { put, post, get } from './api.js';
  import { toast } from './toast.svelte.js';
  import Modal from './Modal.svelte';

  let { settings, username, onClose, onSaved } = $props();

  // svelte-ignore state_referenced_locally
  let s = $state({
    title: 'Heimdeck', theme: 'auto', accent: '#5b8cff', background: '', searchEngine: 'duckduckgo',
    openInNewTab: 'true', showGreeting: 'true',
    ...$state.snapshot(settings),
  });
  let pw = $state({ current: '', new: '' });
  const accents = ['#5b8cff', '#3ecf8e', '#a78bfa', '#f5b942', '#fb7d4b', '#f2555a', '#22c3d6', '#ec4899'];

  async function save() {
    try {
      await put('/settings', $state.snapshot(s));
      toast('Einstellungen gespeichert');
      onSaved();
    } catch (e) {
      toast(e.message, 'bad');
    }
  }

  async function changePassword() {
    try {
      await put('/password', $state.snapshot(pw));
      pw = { current: '', new: '' };
      toast('Passwort geändert');
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
      toast(`${res.tiles} Kachel(n) importiert`);
      onSaved();
    } catch (err) {
      toast(err.message || 'Datei konnte nicht gelesen werden', 'bad');
    }
  }

  async function logout() {
    await post('/logout');
    location.reload();
  }
</script>

<Modal title="Einstellungen" {onClose}>
  <section>
    <h3>Aussehen</h3>
    <label class="field"><span>Titel</span><input bind:value={s.title} /></label>
    <label class="field">
      <span>Farbschema</span>
      <select bind:value={s.theme}>
        <option value="auto">Automatisch (wie System)</option>
        <option value="dark">Dunkel</option>
        <option value="light">Hell</option>
      </select>
    </label>
    <div class="field">
      <span>Akzentfarbe</span>
      <div class="row wrap">
        {#each accents as c}
          <button class="swatch" class:active={s.accent === c} style="background:{c}" onclick={() => (s.accent = c)} aria-label={c}></button>
        {/each}
        <input type="color" bind:value={s.accent} aria-label="Eigene Farbe" />
      </div>
    </div>
    <label class="field">
      <span>Hintergrundbild (URL, optional)</span>
      <input bind:value={s.background} placeholder="https://…/wallpaper.jpg" />
    </label>
    <label class="check"><input type="checkbox" checked={s.showGreeting === 'true'} onchange={(e) => (s.showGreeting = String(e.target.checked))} /> Begrüßung, Datum und Uhrzeit anzeigen</label>
  </section>

  <section>
    <h3>Verhalten</h3>
    <label class="check"><input type="checkbox" checked={s.openInNewTab === 'true'} onchange={(e) => (s.openInNewTab = String(e.target.checked))} /> Links in neuem Tab öffnen</label>
    <label class="field">
      <span>Websuche</span>
      <select bind:value={s.searchEngine}>
        <option value="duckduckgo">DuckDuckGo</option>
        <option value="startpage">Startpage</option>
        <option value="google">Google</option>
        <option value="bing">Bing</option>
      </select>
    </label>
    <div class="row"><span class="spacer"></span><button class="primary" onclick={save}>Speichern</button></div>
  </section>

  <section>
    <h3>Zugang ({username})</h3>
    <div class="grid2">
      <input type="password" placeholder="Aktuelles Passwort" bind:value={pw.current} autocomplete="current-password" />
      <input type="password" placeholder="Neues Passwort" bind:value={pw.new} autocomplete="new-password" />
    </div>
    <div class="row">
      <button onclick={logout}>Abmelden</button>
      <span class="spacer"></span>
      <button disabled={!pw.current || pw.new.length < 8} onclick={changePassword}>Passwort ändern</button>
    </div>
  </section>

  <section>
    <h3>Sicherung</h3>
    <p class="help">Exportiert Gruppen, Kacheln und Einstellungen – API-Keys werden aus Sicherheitsgründen nicht mitgesichert.</p>
    <div class="row">
      <button onclick={exportBackup}>Exportieren</button>
      <label class="button">Importieren<input type="file" accept="application/json" onchange={importBackup} hidden /></label>
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
  .button { cursor: pointer; border: 1px solid var(--border-strong); background: var(--surface-2); border-radius: 10px; padding: 8px 14px; }
  .button:hover { border-color: var(--accent); }
  @media (max-width: 520px) { .grid2 { grid-template-columns: 1fr; } }
</style>
