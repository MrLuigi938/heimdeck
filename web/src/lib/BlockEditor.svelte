<script>
  import Modal from './Modal.svelte';
  import { t } from './i18n.svelte.js';
  import { BLOCK_TYPES, BLOCK_STYLE, blockOf } from './infoBlocks.js';

  // Adding an info block (first its kind, then its options) or changing one.
  // block: the stored block, or null for a new one.
  // sources: { weather: [tiles], calendar: [tiles] } to take the data from.
  // onNewTile(integration): opens the tile editor for a new weather or calendar tile.
  let { block = null, sources = {}, onSave, onClose, onNewTile } = $props();

  // svelte-ignore state_referenced_locally
  let form = $state(block ? structuredClone($state.snapshot(blockOf(block))) : null);
  // svelte-ignore state_referenced_locally
  let creating = !block;

  function pick(type) {
    const def = BLOCK_TYPES[type];
    const list = sources[def.source] || [];
    form = { type, tile: list[0]?.id || 0, opt: { ...def.opt }, style: { ...BLOCK_STYLE } };
  }

  let def = $derived(form ? BLOCK_TYPES[form.type] : null);
  let list = $derived(def?.source ? sources[def.source] || [] : []);
  // A source tile added meanwhile (or the chosen one deleted): take the first one there is.
  $effect(() => {
    if (form && def?.source && list.length && !list.some((x) => x.id === Number(form.tile))) form.tile = list[0].id;
  });

  // Time zones the browser knows, for the second clock.
  const zones = (() => {
    try {
      return Intl.supportedValuesOf('timeZone');
    } catch {
      return ['Europe/Berlin', 'Europe/London', 'America/New_York', 'America/Los_Angeles', 'Asia/Tokyo', 'Australia/Sydney'];
    }
  })();

  function save(e) {
    e?.preventDefault();
    const out = { type: form.type, opt: {}, style: {} };
    if (def.source) out.tile = Number(form.tile) || 0;
    // Only what differs from the defaults is stored.
    for (const [k, v] of Object.entries(form.opt)) if (v !== def.opt[k] && k in def.opt) out.opt[k] = typeof v === 'string' ? v.slice(0, 5000) : v;
    for (const [k, v] of Object.entries(form.style)) if (v !== BLOCK_STYLE[k] && k in BLOCK_STYLE) out.style[k] = v;
    if (!Object.keys(out.opt).length) delete out.opt;
    if (!Object.keys(out.style).length) delete out.style;
    onSave(out);
  }
  const SOURCE_NAME = { weather: 'Wetter', calendar: 'Kalender' };
</script>

<Modal title={form ? `${def.icon} ${t(def.name)}` : t('Block hinzufügen')} {onClose} wide={!form}>
  {#if !form}
    <p class="help intro">{t('Info-Blöcke liegen frei auf der Seite wie die Suche. Ihre Größe bestimmt, wie viel sie zeigen – der Inhalt wächst und schrumpft mit und ragt nie über den Block hinaus.')}</p>
    <div class="gallery">
      {#each Object.entries(BLOCK_TYPES) as [type, d] (type)}
        <button type="button" class="kind" onclick={() => pick(type)}>
          <span class="kicon" aria-hidden="true">{d.icon}</span>
          <b>{t(d.name)}</b>
          <span class="help">{t(d.text)}</span>
        </button>
      {/each}
    </div>
  {:else}
    <form id="block-form" class="form" onsubmit={save}>
      {#if def.source}
        <label class="field">
          <span>{t('Quelle')}</span>
          {#if list.length}
            <select bind:value={form.tile}>
              {#each list as x (x.id)}<option value={x.id}>{x.name}{x.where ? ` – ${x.where}` : ''}</option>{/each}
            </select>
          {:else}
            <span class="help">{form.type === 'weather' ? t('Noch keine Wetter-Kachel. Sie liefert die Daten (Open-Meteo, ohne Konto) und darf in einer ausgeblendeten Gruppe liegen.') : t('Noch keine Kalender-Kachel. Sie liefert die Termine und darf in einer ausgeblendeten Gruppe liegen.')}</span>
          {/if}
          {#if onNewTile}<button type="button" class="small new-tile" onclick={() => onNewTile(def.source)}>+ {t('Neue {name}-Kachel', { name: t(SOURCE_NAME[def.source]) })}</button>{/if}
        </label>
      {/if}

      {#if form.type === 'clock'}
        <div class="field"><span>{t('Zifferblatt')}</span>
          <div class="seg">
            {#each [['digital', 'Digital'], ['analog', 'Analog'], ['word', 'Wortuhr']] as [v, l] (v)}
              <button type="button" class="small" class:on={form.opt.face === v} onclick={() => (form.opt.face = v)}>{t(l)}</button>
            {/each}
          </div>
        </div>
        {#if form.opt.face === 'digital'}
          <div class="field"><span>{t('Ziffern')}</span>
            <div class="seg">
              {#each [['thin', 'Dünn'], ['normal', 'Kräftig'], ['mono', 'Mono'], ['segment', 'Leuchtziffern']] as [v, l] (v)}
                <button type="button" class="small" class:on={form.opt.font === v} onclick={() => (form.opt.font = v)}>{t(l)}</button>
              {/each}
            </div>
          </div>
        {/if}
        {#if form.opt.face !== 'word'}
          <label class="check"><input type="checkbox" bind:checked={form.opt.seconds} /> {t('Sekunden zeigen')}</label>
          <label class="check"><input type="checkbox" bind:checked={form.opt.date} /> {t('Datum zeigen')}</label>
        {/if}
        {#if form.opt.face === 'digital'}
          <label class="check"><input type="checkbox" bind:checked={form.opt.greet} /> {t('Begrüßung darüber („Guten Morgen …“)')}</label>
          <div class="row two">
            <label class="field"><span>{t('Zweite Zeitzone')}</span>
              <input list="block-zones" bind:value={form.opt.tz} placeholder="America/New_York" />
              <datalist id="block-zones">{#each zones as z (z)}<option value={z}></option>{/each}</datalist>
            </label>
            <label class="field"><span>{t('Name dafür')} <span class="opt">({t('optional')})</span></span><input bind:value={form.opt.tzName} maxlength="40" placeholder={t('z. B. Büro')} /></label>
          </div>
        {/if}
      {:else if form.type === 'weather'}
        <label class="check"><input type="checkbox" bind:checked={form.opt.mood} /> {t('Stimmung: Himmel als Hintergrund, je nach Wetter und Tageszeit')}</label>
        <label class="check"><input type="checkbox" bind:checked={form.opt.days} /> {t('Nächste Tage zeigen (wenn Platz ist)')}</label>
        <label class="field"><span>{t('Stundenverlauf')}</span>
          <select bind:value={form.opt.hours}>
            <option value={12}>{t('12 Stunden')}</option>
            <option value={24}>{t('24 Stunden')}</option>
          </select>
        </label>
      {:else if form.type === 'text'}
        <div class="field"><span>{t('Art')}</span>
          <div class="seg">
            {#each [['plain', 'Text'], ['heading', 'Überschrift'], ['note', 'Notizzettel'], ['quote', 'Zitat']] as [v, l] (v)}
              <button type="button" class="small" class:on={form.opt.look === v} onclick={() => (form.opt.look = v)}>{t(l)}</button>
            {/each}
          </div>
        </div>
        <label class="field"><span>{t('Text')}</span>
          <textarea rows={form.opt.look === 'heading' ? 2 : 6} bind:value={form.opt.text} maxlength="5000"></textarea>
          {#if form.opt.look !== 'heading'}<span class="help">{t('**fett**, *kursiv*, Listen mit „- “ und Links gehen.')}</span>{/if}
        </label>
      {:else if form.type === 'countdown'}
        <div class="row two">
          <label class="field"><span>{t('Datum')}</span><input type="date" bind:value={form.opt.date} required /></label>
          <label class="field"><span>{t('Wofür')}</span><input bind:value={form.opt.label} maxlength="60" placeholder={t('z. B. Urlaub 🏖')} /></label>
        </div>
        <label class="check"><input type="checkbox" bind:checked={form.opt.yearly} /> {t('Jedes Jahr (Geburtstag, Jahrestag)')}</label>
        {#if !form.opt.yearly}
          <label class="field"><span>{t('Fortschritt ab')} <span class="opt">({t('optional')})</span></span><input type="date" bind:value={form.opt.start} />
            <span class="help">{t('Mit einem Startdatum zeigt ein Balken, wie viel der Wartezeit schon um ist.')}</span></label>
        {/if}
      {:else if form.type === 'status'}
        <p class="help">{t('Zeigt, ob alle überwachten Dienste dieses Dashboards erreichbar sind – oder welche gestört sind. Ein Klick auf einen Dienst springt zu seiner Gruppe.')}</p>
      {/if}

      <fieldset class="look">
        <legend>{t('Aussehen')}</legend>
        <div class="field"><span>{t('Hintergrund')}</span>
          <div class="seg">
            {#each [['card', 'Karte'], ['glass', 'Glas'], ['none', 'Ohne']] as [v, l] (v)}
              <button type="button" class="small" class:on={form.style.bg === v} onclick={() => (form.style.bg = v)}>{t(l)}</button>
            {/each}
          </div>
        </div>
        <div class="field"><span>{t('Schriftgröße')}</span>
          <div class="seg">
            {#each [[0.8, 'Kleiner'], [1, 'Passend'], [1.2, 'Größer'], [1.45, 'Groß']] as [v, l] (v)}
              <button type="button" class="small" class:on={form.style.scale === v} onclick={() => (form.style.scale = v)}>{t(l)}</button>
            {/each}
          </div>
        </div>
        {#if form.type !== 'weather' && form.type !== 'agenda'}
          <div class="field"><span>{t('Ausrichtung')}</span>
            <div class="seg">
              {#each [['', 'Passend'], ['left', 'Linksbündig'], ['center', 'Zentriert'], ['right', 'Rechtsbündig']] as [v, l] (v)}
                <button type="button" class="small" class:on={form.style.align === v} onclick={() => (form.style.align = v)}>{t(l)}</button>
              {/each}
            </div>
          </div>
        {/if}
        <div class="row accent">
          <label class="check"><input type="checkbox" checked={!!form.style.accent} onchange={(e) => (form.style.accent = e.currentTarget.checked ? '#5b8cff' : '')} /> {t('Eigene Akzentfarbe')}</label>
          {#if form.style.accent}<input type="color" bind:value={form.style.accent} aria-label={t('Akzentfarbe')} />{/if}
        </div>
      </fieldset>
    </form>
  {/if}

  {#snippet footer()}
    {#if form && creating}<button type="button" onclick={() => (form = null)}>← {t('Andere Art')}</button>{/if}
    <span class="spacer"></span>
    <button type="button" onclick={onClose}>{t('Abbrechen')}</button>
    {#if form}<button class="primary" type="submit" form="block-form">{creating ? t('Hinzufügen') : t('Speichern')}</button>{/if}
  {/snippet}
</Modal>

<style>
  .intro { margin: 0 0 12px; }
  .gallery { display: grid; grid-template-columns: repeat(auto-fill, minmax(190px, 1fr)); gap: 10px; }
  .kind { display: grid; gap: 4px; justify-items: start; text-align: left; padding: 12px 14px; border-radius: var(--r-md); }
  .kicon { font-size: 26px; line-height: 1; }
  .form { display: grid; gap: 14px; }
  .field { display: grid; gap: 6px; font-size: 13px; color: var(--muted); }
  .field > span { font-weight: 500; }
  .opt { font-weight: 400; }
  .seg { display: flex; flex-wrap: wrap; gap: 4px; }
  .seg button.on { border-color: var(--accent); background: color-mix(in srgb, var(--accent) 18%, transparent); color: var(--text); }
  .two { display: grid; grid-template-columns: 1fr 1fr; gap: 10px; align-items: start; }
  .new-tile { justify-self: start; }
  .look { border: 1px solid var(--border); border-radius: var(--r-md); padding: 10px 12px 12px; display: grid; gap: 12px; margin: 0; }
  .look legend { font-size: 12px; color: var(--muted); padding: 0 6px; }
  .accent { gap: 12px; }
  .accent input[type='color'] { width: 44px; height: 30px; padding: 2px; border-radius: var(--r-xs); }
  @media (max-width: 520px) { .two { grid-template-columns: 1fr; } }
</style>
