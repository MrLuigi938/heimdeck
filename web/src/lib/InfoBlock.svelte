<script>
  import { get } from './api.js';
  import { t, i18n } from './i18n.svelte.js';
  import { BLOCK_TYPES, shapeOf } from './infoBlocks.js';
  import ClockBlock from './ClockBlock.svelte';
  import WeatherBlock from './WeatherBlock.svelte';
  import StatusBlock from './StatusBlock.svelte';
  import AgendaBlock from './AgendaBlock.svelte';
  import TextBlock from './TextBlock.svelte';
  import CountdownBlock from './CountdownBlock.svelte';
  import Chart from './Chart.svelte';

  // One info block on the page. It keeps the size the grid gives it – the
  // content scales with that size (container units) and is cut off inside
  // rather than ever reaching over the tiles next to it.
  // block: { type, tile, opt, style } with the defaults filled in; it: { w, h } in grid cells.
  // source: the tile the data comes from (weather, calendar), or null.
  let { block, it, source = null, status = {}, boardTiles = [], groups = [], greeting = '', onJump, onSetup = null } = $props();

  let def = $derived(BLOCK_TYPES[block.type]);
  let shape = $derived(shapeOf(it));
  let style = $derived(block.style);

  // ---- data from the tile (weather, calendar) ----
  let widget = $state(null);
  let error = $state('');
  let loaded = $state(false);
  $effect(() => {
    if (!def?.source || !source) return;
    source.id; i18n.lang;
    let stopped = false;
    let retry;
    const load = async () => {
      try {
        const res = await get(`/tiles/${source.id}/widget`);
        if (stopped) return;
        if (res.pending) {
          retry = setTimeout(load, 2500);
          return;
        }
        widget = res.widget;
        error = res.error || '';
      } catch (e) {
        if (!stopped) error = e.message;
      }
      loaded = true;
    };
    loaded = false;
    widget = null;
    load();
    // The server keeps weather for ten minutes and calendars for fifteen.
    const timer = setInterval(() => document.visibilityState === 'visible' && load(), 120000);
    return () => {
      stopped = true;
      clearInterval(timer);
      clearTimeout(retry);
    };
  });

  const ALIGN = { left: 'flex-start', center: 'center', right: 'flex-end' };
  // The chart chosen in a chart block (or the tile's first one).
  let chart = $derived(block.type === 'chart' ? widget?.charts?.find((c) => c.id === block.opt?.chart) || widget?.charts?.[0] || null : null);
</script>

<div class="ib bg-{style.bg} type-{block.type}" class:card={style.bg === 'card'} style:--ib-scale={style.scale || 1} style:--ib-accent={style.accent || null} style:--ib-align={style.align || null} style:--ib-items={ALIGN[style.align] || null}>
  {#if block.type === 'clock'}
    <ClockBlock opt={block.opt} {shape} {greeting} />
  {:else if block.type === 'status'}
    <StatusBlock tiles={boardTiles} {status} {groups} {shape} {onJump} />
  {:else if block.type === 'text'}
    <TextBlock opt={block.opt} {shape} />
  {:else if block.type === 'countdown'}
    <CountdownBlock opt={block.opt} {shape} />
  {:else if def?.source && !source}
    <div class="hint">
      <span>{def.icon} {block.type === 'weather' ? t('Wähle eine Wetter-Kachel als Quelle.') : block.type === 'chart' ? t('Wähle eine Kachel mit Diagrammen als Quelle.') : t('Wähle eine Kalender-Kachel als Quelle.')}</span>
      {#if onSetup}<button type="button" class="small" onclick={onSetup}>{t('Einrichten')}</button>{/if}
    </div>
  {:else if !loaded}
    <div class="hint muted">{def.icon} {t('Lade …')}</div>
  {:else if block.type === 'weather' && widget?.weather}
    <WeatherBlock w={widget.weather} opt={block.opt} {shape} />
  {:else if block.type === 'chart' && chart}
    <div class="chartbox"><Chart {chart} /></div>
  {:else if block.type === 'agenda' && widget}
    <AgendaBlock {widget} {shape} />
  {:else}
    <div class="hint" title={error}>⚠ {error || t('Keine Daten')}</div>
  {/if}
</div>

<style>
  /* A size container: the blocks scale with cqw/cqh. contain: size in the page
     grid keeps the content from making the block taller. */
  .ib { --ib-accent: var(--accent); position: relative; height: 100%; width: 100%; min-height: 0; container-type: size; overflow: hidden; border-radius: var(--radius);
    color: var(--text); box-sizing: border-box; }
  /* "card" takes the theme's card (frames, glass … of each theme). */
  .bg-glass { background: color-mix(in srgb, var(--surface-solid) 38%, transparent); border: 1px solid color-mix(in srgb, var(--text) 12%, transparent);
    -webkit-backdrop-filter: blur(16px) saturate(1.3); backdrop-filter: blur(16px) saturate(1.3); }
  /* Without a card the text stands on the page; on a picture a soft shadow keeps it readable. */
  .bg-none { background: none; border: 0; }
  :global(body.has-bg) .bg-none { text-shadow: 0 1px 3px color-mix(in srgb, var(--bg) 85%, transparent), 0 0 12px color-mix(in srgb, var(--bg) 60%, transparent); }
  /* The picture decides, not the theme: a light theme can have a dark photo and
     the other way round (body data-bg-tone, measured from the picture). */
  :global(body.has-bg[data-bg-tone='dark']) .bg-none {
    --text: #f3f5f8; --muted: color-mix(in srgb, #f3f5f8 78%, transparent);
    text-shadow: 0 1px 3px rgba(0, 0, 0, .85), 0 0 12px rgba(0, 0, 0, .6);
  }
  :global(body.has-bg[data-bg-tone='light']) .bg-none {
    --text: #14161b; --muted: color-mix(in srgb, #14161b 72%, transparent);
    text-shadow: 0 1px 3px rgba(255, 255, 255, .85), 0 0 12px rgba(255, 255, 255, .6);
  }
  .chartbox { height: 100%; padding: clamp(6px, 3cqmin, 14px); box-sizing: border-box; display: flex; flex-direction: column; min-height: 0; }
  .hint { height: 100%; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 8px; padding: 10px; text-align: center; font-size: 13px; opacity: .8; box-sizing: border-box; overflow: hidden; }
</style>
