<script>
  import { post, put, del } from './api.js';
  import { toast } from './toast.svelte.js';
  import Modal from './Modal.svelte';
  import Icon from './Icon.svelte';
  import Widget from './Widget.svelte';
  import { t } from './i18n.svelte.js';
  import { confirmDialog } from './dialog.svelte.js';

  let { tile, groups, integrations, onClose, onSaved } = $props();

  // A fresh copy so cancelling leaves the dashboard untouched.
  // svelte-ignore state_referenced_locally
  let form = $state({
    id: 0, groupId: groups[0]?.id, name: '', url: '', externalUrl: '', apiUrl: '', icon: '', description: '',
    integration: '', config: {}, insecure: false, showWidget: true, monitor: true, secretsSet: [],
    ...structuredClone($state.snapshot(tile)),
  });
  form.config ??= {};
  let secrets = $state({});
  let step = $state(form.integration || form.id ? 'form' : 'pick');
  let filter = $state('');
  let advanced = $state(!!(form.apiUrl || form.insecure));
  let testing = $state(false);
  let test = $state(null);
  let saving = $state(false);

  let def = $derived(integrations.find((d) => d.id === (form.integration || 'link')));
  let general = $derived(t('Allgemein'));
  let categories = $derived.by(() => {
    const f = filter.trim().toLowerCase();
    const list = integrations.filter((d) => !f || d.name.toLowerCase().includes(f) || d.category.toLowerCase().includes(f));
    const out = {};
    for (const d of list) (out[d.category] ??= []).push(d);
    return Object.entries(out).sort(([a], [b]) => (a === general ? -1 : b === general ? 1 : a.localeCompare(b)));
  });

  function pick(d) {
    form.integration = d.id;
    if (!form.name || integrations.some((x) => x.name.split(' /')[0] === form.name)) form.name = d.id === 'link' ? '' : d.name.split(' /')[0];
    if (!form.icon || integrations.some((x) => x.icon === form.icon)) form.icon = d.icon;
    test = null;
    step = 'form';
  }

  function payload() {
    return { ...$state.snapshot(form), secrets: $state.snapshot(secrets) };
  }

  async function runTest() {
    testing = true;
    test = null;
    try {
      test = await post('/tiles/test', payload());
    } catch (e) {
      test = { ok: false, error: e.message };
    }
    testing = false;
  }

  async function save(e) {
    e?.preventDefault();
    saving = true;
    try {
      if (form.id) await put(`/tiles/${form.id}`, payload());
      else await post('/tiles', payload());
      toast(form.id ? t('Gespeichert') : t('„{name}“ hinzugefügt', { name: form.name }));
      onSaved();
    } catch (err) {
      toast(err.message, 'bad');
    }
    saving = false;
  }

  async function remove() {
    if (!(await confirmDialog(t('„{name}“ wirklich löschen?', { name: form.name }), { danger: true }))) return;
    try {
      await del(`/tiles/${form.id}`);
      toast(t('Gelöscht'));
      onSaved();
    } catch (err) {
      toast(err.message, 'bad');
    }
  }
</script>

<Modal title={form.id ? t('{name} bearbeiten', { name: form.name }) : t('Kachel hinzufügen')} {onClose} wide={step === 'pick'}>
  {#if step === 'pick'}
    <!-- svelte-ignore a11y_autofocus -->
    <input placeholder={t('Dienst suchen … (z. B. Radarr)')} bind:value={filter} autofocus />
    {#each categories as [cat, defs]}
      <div class="cat">{cat}</div>
      <div class="picker">
        {#each defs as d}
          <button class="pick" onclick={() => pick(d)}>
            <Icon icon={d.icon} name={d.name} size={30} />
            <span class="pick-name">{d.name}</span>
            {#if d.hasWidget}<span class="tag">{t('Live-Daten')}</span>{/if}
          </button>
        {/each}
      </div>
    {/each}
  {:else}
    <form id="tile-form" onsubmit={save} class="form">
      <button type="button" class="type" onclick={() => (step = 'pick')}>
        <Icon icon={def?.icon} name={def?.name} size={28} />
        <div class="grow">
          <div><b>{def?.name}</b></div>
          {#if def?.description}<div class="help">{def.description}</div>{/if}
        </div>
        <span class="muted">{t('ändern')}</span>
      </button>

      <div class="grid2">
        <label class="field"><span>{t('Name')}</span><input bind:value={form.name} required /></label>
        <label class="field">
          <span>{t('Gruppe')}</span>
          <select bind:value={form.groupId}>
            {#each groups as g}<option value={g.id}>{g.name}</option>{/each}
          </select>
        </label>
      </div>

      <div class="grid2">
        <label class="field">
          <span>{t('Lokale Adresse')}</span>
          <input bind:value={form.url} placeholder="http://192.168.1.10:7878" inputmode="url" />
          <span class="help">{t('Wird zuhause geöffnet.')}</span>
        </label>
        <label class="field">
          <span>{t('Öffentliche Adresse (optional)')}</span>
          <input bind:value={form.externalUrl} placeholder={t('https://radarr.deinedomain.de')} inputmode="url" />
          <span class="help">{t('Wird unterwegs geöffnet und ebenfalls überwacht.')}</span>
        </label>
      </div>

      {#each def?.fields || [] as f}
        {#if f.type === 'checkbox'}
          <label class="check">
            <input type="checkbox" checked={form.config[f.key] === 'true'} onchange={(e) => (form.config[f.key] = e.currentTarget.checked ? 'true' : '')} />
            {f.label}
          </label>
          {#if f.help}<span class="help check-help">{f.help}</span>{/if}
        {:else}
        <label class="field">
          <span>{f.label}{f.required ? '' : ` (${t('optional')})`}</span>
          {#if f.secret}
            <input type="password" bind:value={secrets[f.key]} autocomplete="off"
              placeholder={form.secretsSet?.includes(f.key) ? '•••••••• ' + t('gespeichert – leer lassen zum Beibehalten') : ''} />
          {:else}
            <input bind:value={form.config[f.key]} autocomplete="off" />
          {/if}
          {#if f.help}<span class="help">{f.help}</span>{/if}
        </label>
        {/if}
      {/each}

      {#if def?.hasWidget}
        <label class="check"><input type="checkbox" bind:checked={form.showWidget} /> {t('Live-Daten auf der Kachel anzeigen')}</label>
      {/if}
      <label class="check"><input type="checkbox" bind:checked={form.monitor} /> {t('Online-Status prüfen und anzeigen')}</label>

      <button type="button" class="ghost small more" onclick={() => (advanced = !advanced)}>{advanced ? '▾' : '▸'} {t('Weitere Optionen')}</button>
      {#if advanced}
        <div class="advanced">
          <div class="grid2">
            <label class="field">
              <span>{t('Icon')}</span>
              <div class="row"><Icon icon={form.icon} name={form.name} size={34} /><input bind:value={form.icon} placeholder={t('radarr, URL oder Emoji')} /></div>
              <span class="help">{t('Name aus')} <a href="https://dashboardicons.com" target="_blank" rel="noopener noreferrer">dashboardicons.com</a>, {t('Bild-URL oder Emoji')}</span>
            </label>
            <label class="field"><span>{t('Beschreibung')}</span><input bind:value={form.description} placeholder={t('z. B. Filme')} /></label>
          </div>
          <label class="field">
            <span>{t('Interne Adresse (optional)')}</span>
            <input bind:value={form.apiUrl} placeholder="http://radarr:7878" />
            <span class="help">{t('Falls das Dashboard den Dienst anders erreicht als dein Browser – z. B. über den Containernamen im selben Docker-Netzwerk.')}</span>
          </label>
          <label class="check"><input type="checkbox" bind:checked={form.insecure} /> {t('Zertifikat nicht prüfen (selbstsignierte HTTPS-Zertifikate)')}</label>
        </div>
      {/if}

      {#if test}
        <div class="test {test.ok ? 'pass' : 'fail'}">
          {#if test.ok}
            ✓ {t('Verbindung erfolgreich')}{test.ms != null ? ` (${test.ms} ms)` : ''}
            {#if test.widget}<div class="preview"><Widget widget={test.widget} /></div>{/if}
          {:else}
            ✕ {test.error}
          {/if}
        </div>
      {/if}
    </form>
  {/if}

  {#snippet footer()}
    {#if step === 'form'}
      {#if form.id}<button class="danger" onclick={remove}>{t('Löschen')}</button>{/if}
      <span class="spacer"></span>
      <button onclick={runTest} disabled={testing || (!form.url && !form.apiUrl)}>{testing ? t('Teste …') : t('Verbindung testen')}</button>
      <button class="primary" type="submit" form="tile-form" disabled={saving}>{t('Speichern')}</button>
    {:else}
      <span class="help">{t("Nicht dabei? Einfach „Nur Link“ wählen – Status-Anzeige gibt es trotzdem.")}</span>
    {/if}
  {/snippet}
</Modal>

<style>
  .cat { font-size: 11.5px; text-transform: uppercase; letter-spacing: .06em; color: var(--muted); margin-bottom: -6px; }
  .picker { display: grid; grid-template-columns: repeat(auto-fill, minmax(150px, 1fr)); gap: 8px; }
  .pick { display: flex; flex-direction: column; align-items: center; gap: 6px; padding: 14px 8px; background: var(--surface-2); }
  .pick-name { font-size: 13.5px; text-align: center; }
  .tag { font-size: 10.5px; color: var(--accent); }
  .form { display: grid; gap: 14px; }
  .type { display: flex; gap: 12px; align-items: center; text-align: left; padding: 10px 12px; }
  .grow { flex: 1; min-width: 0; }
  .grid2 { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; }
  .more { justify-self: start; color: var(--muted); padding-left: 0; }
  .advanced { display: grid; gap: 14px; padding: 12px; border: 1px solid var(--border); border-radius: 12px; }
  .help a { color: var(--accent); }
  .check-help { display: block; margin: -4px 0 4px 26px; }
  .test { padding: 12px; border-radius: 12px; font-size: 14px; }
  .pass { background: color-mix(in srgb, var(--ok) 12%, transparent); color: var(--ok); }
  .fail { background: color-mix(in srgb, var(--bad) 12%, transparent); color: var(--bad); }
  .preview { margin-top: 10px; color: var(--text); }
  @media (max-width: 520px) { .grid2 { grid-template-columns: 1fr; } }
</style>
