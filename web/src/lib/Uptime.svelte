<script>
  import { get } from './api.js';
  import Icon from './Icon.svelte';
  import { t, locale } from './i18n.svelte.js';

  // Status history of every tile: one bar per address (local and public),
  // 48 half-hour slots for 24 hours or 72 hourly slots for three days.
  let { tiles, status = {} } = $props();

  const KEY = 'heimdeck-uptime';
  let prefs = $state(readPrefs());
  let data = $state(null);
  let error = $state('');

  function readPrefs() {
    try {
      return { range: '24h', open: true, ...JSON.parse(localStorage.getItem(KEY) || '{}') };
    } catch {
      return { range: '24h', open: true };
    }
  }
  function savePrefs() {
    try {
      localStorage.setItem(KEY, JSON.stringify(prefs));
    } catch {
      /* private mode */
    }
  }

  async function load() {
    try {
      data = await get('/uptime?range=' + prefs.range);
      error = '';
    } catch (e) {
      error = e.message;
    }
  }

  $effect(() => {
    if (!prefs.open) return;
    prefs.range;
    load();
    const t = setInterval(() => document.visibilityState === 'visible' && load(), 60000);
    return () => clearInterval(t);
  });

  let byId = $derived(Object.fromEntries(tiles.map((tile) => [tile.id, tile])));
  let rows = $derived((data?.rows || []).filter((r) => byId[r.tileId]));
  // The headline reflects the current state, not the history.
  let summary = $derived.by(() => {
    const current = tiles.map((tile) => status[tile.id]).filter(Boolean);
    const down = current.filter((s) => !s.online || (s.ext && !s.ext.online)).length;
    return { total: current.length, down };
  });

  const fmtTime = (unix) => new Date(unix * 1000).toLocaleString(locale(), { weekday: 'short', hour: '2-digit', minute: '2-digit' });
  const fmtPct = (p) => (p >= 99.95 ? '100' : p.toFixed(p >= 99 ? 2 : 1).replace('.', ',')) + ' %';

  function slotClass(b) {
    if (!b) return 'none';
    if (b.up === b.total) return 'up';
    if (b.up === 0) return 'down';
    return 'partial';
  }

  function slotTitle(b, i) {
    const start = data.start + i * data.slot;
    const range = `${fmtTime(start)} – ${new Date((start + data.slot) * 1000).toLocaleTimeString(locale(), { hour: '2-digit', minute: '2-digit' })}`;
    if (!b) return `${range}\n${t('Keine Daten')}`;
    const ok = b.up === b.total ? t('immer erreichbar') : t('{n} von {total} Prüfungen fehlgeschlagen', { n: b.total - b.up, total: b.total });
    return `${range}\n${ok}${b.up ? `\nØ ${b.ms} ms` : ''}`;
  }

  function relDown(unix) {
    const mins = Math.round((Date.now() / 1000 - unix) / 60);
    if (mins < 2) return t('gerade eben');
    if (mins < 60) return t('vor {n} Min.', { n: mins });
    if (mins < 60 * 24) return t('vor {n} Std.', { n: Math.round(mins / 60) });
    return t('vor {n} Tagen', { n: Math.round(mins / 1440) });
  }

  function toggle() {
    prefs.open = !prefs.open;
    savePrefs();
  }
  function setRange(r) {
    prefs.range = r;
    savePrefs();
  }
</script>

<section class="uptime">
  <div class="head">
    <button class="title on-bg" onclick={toggle} aria-expanded={prefs.open}>
      <span class="chev">{prefs.open ? '▾' : '▸'}</span> {t('Verfügbarkeit')}
      {#if data && summary.total}
        <span class="sum {summary.down ? 'bad' : 'ok'}">
          {summary.down ? t('{n} gestört', { n: summary.down }) : t('alles erreichbar')}
        </span>
      {/if}
    </button>
    {#if prefs.open}
      <div class="seg on-bg">
        <button class:on={prefs.range === '24h'} onclick={() => setRange('24h')}>{t('24 Std.')}</button>
        <button class:on={prefs.range === '72h'} onclick={() => setRange('72h')}>{t('3 Tage')}</button>
      </div>
    {/if}
  </div>

  {#if prefs.open}
    <div class="card body">
      {#if error}
        <div class="bad">{error}</div>
      {:else if !data}
        <div class="muted">{t('Lade …')}</div>
      {:else if rows.length === 0}
        <div class="muted">{t('Noch keine Dienste mit Adresse.')}</div>
      {:else}
        {#each rows as r (r.tileId + r.kind)}
          {@const tile = byId[r.tileId]}
          <div class="row-u">
            <div class="who">
              <Icon icon={tile.icon} name={tile.name} size={22} />
              <div class="names">
                <div class="name">{tile.name}</div>
                <div class="kind" title={r.url}>{r.kind === 'ext' ? '🌐 ' + t('Öffentlich') : '🏠 ' + t('Lokal')}</div>
              </div>
            </div>
            <div class="bar" style="--n:{r.buckets.length}">
              {#each r.buckets as b, i}
                <span class="slot {slotClass(b)}" title={slotTitle(b, i)}></span>
              {/each}
            </div>
            <div class="nums">
              {#if r.uptime != null}
                <div class="pct {r.uptime < 99 ? (r.uptime < 90 ? 'bad' : 'warn') : ''}">{fmtPct(r.uptime)}</div>
                <div class="meta">
                  {r.avgMs ? `Ø ${r.avgMs} ms` : ''}{r.lastDown ? ` · ${t('Ausfall')} ${relDown(r.lastDown)}` : ''}
                </div>
              {:else}
                <div class="meta">{t('sammelt Daten …')}</div>
              {/if}
            </div>
          </div>
        {/each}
        <div class="legend">
          <span><i class="slot up"></i> {t('erreichbar')}</span>
          <span><i class="slot partial"></i> {t('kurz gestört')}</span>
          <span><i class="slot down"></i> {t('ausgefallen')}</span>
          <span><i class="slot none"></i> {t('keine Daten')}</span>
          <span class="spacer"></span>
          <span>{t('Prüfung jede Minute')}</span>
        </div>
      {/if}
    </div>
  {/if}
</section>

<style>
  .uptime { display: grid; gap: 12px; }
  .head { display: flex; align-items: center; gap: 8px; min-height: 30px; }
  .title { background: none; border: 0; padding: 0; font-size: 13px; text-transform: uppercase; letter-spacing: .08em; color: var(--muted); font-weight: 600; display: flex; gap: 8px; align-items: center; }
  .chev { width: 10px; }
  .sum { text-transform: none; letter-spacing: 0; font-weight: 500; font-size: 12px; padding: 1px 8px; border-radius: 8px; background: var(--surface-2); }
  .seg { margin-left: auto; display: flex; border: 1px solid var(--border); border-radius: 10px; overflow: hidden; }
  .seg button { border: 0; border-radius: 0; padding: 4px 10px; font-size: 12.5px; background: transparent; color: var(--muted); }
  .seg button.on { background: var(--surface-2); color: var(--text); }
  .body { padding: 8px 16px 12px; display: grid; }
  .row-u { display: grid; grid-template-columns: minmax(120px, 200px) minmax(0, 1fr) 170px; gap: 16px; align-items: center; padding: 8px 0; border-top: 1px solid var(--border); }
  .row-u:first-child { border-top: 0; }
  .who { display: flex; gap: 10px; align-items: center; min-width: 0; }
  .names { min-width: 0; }
  .name { font-size: 13.5px; font-weight: 500; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .kind { font-size: 11.5px; color: var(--muted); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .bar { display: grid; grid-template-columns: repeat(var(--n), minmax(0, 1fr)); gap: 2px; height: 26px; }
  .slot { display: block; border-radius: 2px; background: var(--surface-2); }
  .slot.up { background: var(--ok); opacity: .85; }
  .slot.partial { background: var(--warn); }
  .slot.down { background: var(--bad); }
  .slot:hover { opacity: 1; outline: 1px solid var(--text); }
  .nums { text-align: right; min-width: 0; }
  .pct { font-weight: 650; font-variant-numeric: tabular-nums; }
  .meta { font-size: 11.5px; color: var(--muted); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .legend { display: flex; flex-wrap: wrap; gap: 6px 14px; font-size: 11.5px; color: var(--muted); padding-top: 10px; border-top: 1px solid var(--border); margin-top: 4px; }
  .legend span { display: inline-flex; align-items: center; gap: 5px; }
  .legend i { width: 10px; height: 10px; display: inline-block; }
  @media (max-width: 720px) {
    .row-u { grid-template-columns: minmax(0, 1fr) auto; gap: 6px 12px; }
    .bar { grid-column: 1 / -1; grid-row: 2; height: 20px; gap: 1px; }
  }
</style>
