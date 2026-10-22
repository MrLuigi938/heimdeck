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

  // Pick list for fields like Home Assistant entities: search and tick instead of typing IDs.
  let picking = $state('');
  let pickQuery = $state('');
  let choices = $state(null);
  let pickError = $state('');
  let pickLoading = $state(false);
  let pickTimer;
  let pickSeq = 0;

  const ids = (key) => (form.config[key] || '').split(/[\s,;]+/).filter(Boolean);

  function openPicker(key) {
    if (picking === key) {
      picking = '';
      return;
    }
    picking = key;
    pickQuery = '';
    loadChoices();
  }

  async function loadChoices() {
    const seq = ++pickSeq;
    pickLoading = true;
    pickError = '';
    try {
      const list = await post('/tiles/choices', { ...payload(), field: picking, query: pickQuery });
      if (seq === pickSeq) choices = list;
    } catch (e) {
      if (seq === pickSeq) { choices = null; pickError = e.message; }
    }
    if (seq === pickSeq) pickLoading = false;
  }

  function searchChoices() {
    clearTimeout(pickTimer);
    pickTimer = setTimeout(loadChoices, 300);
  }

  function toggleChoice(key, id) {
    const list = ids(key);
    form.config[key] = (list.includes(id) ? list.filter((x) => x !== id) : [...list, id]).join(', ');
  }

  let groupedChoices = $derived.by(() => {
    const out = {};
    for (const c of choices || []) (out[c.group || ''] ??= []).push(c);
    return Object.entries(out);
  });

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

      {#if def?.setup?.length}
        <details class="setup" open={!form.id}>
          <summary>{t('So richtest du {name} ein', { name: def.name })}</summary>
          <ol>{#each def.setup as step}<li>{step}</li>{/each}</ol>
        </details>
      {/if}

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
          {#if f.secret && f.type === 'textarea'}
            <textarea rows="2" bind:value={secrets[f.key]} autocomplete="off" spellcheck="false"
              placeholder={form.secretsSet?.includes(f.key) ? '•••••••• ' + t('gespeichert – leer lassen zum Beibehalten') : ''}></textarea>
          {:else if f.secret}
            <input type="password" bind:value={secrets[f.key]} autocomplete="off"
              placeholder={form.secretsSet?.includes(f.key) ? '•••••••• ' + t('gespeichert – leer lassen zum Beibehalten') : ''} />
          {:else if f.type === 'textarea'}
            <textarea rows="3" bind:value={form.config[f.key]} autocomplete="off" spellcheck="false"></textarea>
          {:else if f.type === 'select'}
            <select bind:value={form.config[f.key]}>
              {#each f.options || [] as o}<option value={o}>{o}</option>{/each}
            </select>
          {:else if f.pick}
            <div class="row">
              <input bind:value={form.config[f.key]} autocomplete="off" />
              <button type="button" class:primary={picking === f.key} onclick={() => openPicker(f.key)}
                disabled={!form.url && !form.apiUrl}>{t('Auswählen')}</button>
            </div>
          {:else}
            <input bind:value={form.config[f.key]} autocomplete="off" />
          {/if}
          {#if f.help}<span class="help">{f.help}</span>{/if}
        </label>
        {#if picking === f.key}
          <div class="chooser">
            <!-- svelte-ignore a11y_autofocus -->
            <input type="search" placeholder={t('Suchen … (Name oder ID)')} bind:value={pickQuery} oninput={searchChoices} autofocus />
            {#if pickError}
              <div class="help bad">{pickError}</div>
            {:else if choices && !choices.length && !pickLoading}
              <div class="help">{pickQuery ? t('Nichts gefunden.') : t('Gib einen Suchbegriff ein, z. B. einen Raum oder Gerätenamen.')}</div>
            {:else if choices}
              <div class="choices" class:dim={pickLoading}>
                {#each groupedChoices as [group, list]}
                  {#if group}<div class="cgroup">{group}</div>{/if}
                  {#each list as c}
                    <label class="choice">
                      <input type="checkbox" checked={ids(f.key).includes(c.id)} onchange={() => toggleChoice(f.key, c.id)} />
                      <span class="cname">
                        <span>{c.name || c.id}</span>
                        {#if c.name}<span class="cid">{c.id}</span>{/if}
                      </span>
                      {#if c.value}<span class="cval">{c.value}</span>{/if}
                    </label>
                  {/each}
                {/each}
              </div>
            {:else}
              <div class="help">{t('Lade …')}</div>
            {/if}
          </div>
        {/if}
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
      <button onclick={runTest} disabled={testing || (!form.url && !form.apiUrl && !def?.noUrl)}>{testing ? t('Teste …') : t('Verbindung testen')}</button>
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
  /* align-items: start keeps both inputs of a row at the same height and line,
     even when only one of them has a help text below. */
  .grid2 { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; align-items: start; }
  .more { justify-self: start; color: var(--muted); padding-left: 0; }
  .advanced { display: grid; gap: 14px; padding: 12px; border: 1px solid var(--border); border-radius: 12px; }
  .help a { color: var(--accent); }
  .check-help { display: block; margin: -4px 0 4px 26px; }
  textarea { width: 100%; resize: vertical; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 12.5px; line-height: 1.5; }
  .setup { background: var(--surface-2); border-radius: 12px; padding: 10px 14px; font-size: 13.5px; }
  .setup summary { cursor: pointer; font-weight: 600; }
  .setup ol { margin: 8px 0 2px; padding-left: 20px; display: grid; gap: 6px; color: var(--muted); }
  .row { display: flex; gap: 8px; align-items: center; }
  .row input { flex: 1; min-width: 0; }
  .chooser { display: grid; gap: 8px; margin-top: -6px; padding: 10px; border: 1px solid var(--border); border-radius: 12px; }
  .choices { max-height: 280px; overflow: auto; display: grid; gap: 2px; }
  .choices.dim { opacity: .6; }
  .cgroup { font-size: 11px; text-transform: uppercase; letter-spacing: .06em; color: var(--muted); margin: 6px 0 2px; }
  .choice { display: flex; gap: 10px; align-items: center; padding: 5px 6px; border-radius: 8px; cursor: pointer; font-size: 13.5px; }
  .choice:hover { background: var(--surface-2); }
  .cname { flex: 1; min-width: 0; display: grid; }
  .cname > span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .cid { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 11.5px; color: var(--muted); }
  .cval { color: var(--muted); font-size: 12.5px; white-space: nowrap; }
  .bad { color: var(--bad); }
  .test { padding: 12px; border-radius: 12px; font-size: 14px; }
  .pass { background: color-mix(in srgb, var(--ok) 12%, transparent); color: var(--ok); }
  .fail { background: color-mix(in srgb, var(--bad) 12%, transparent); color: var(--bad); }
  .preview { margin-top: 10px; color: var(--text); }
  @media (max-width: 520px) { .grid2 { grid-template-columns: 1fr; } }
</style>
