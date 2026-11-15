<script>
  import { t } from './i18n.svelte.js';

  // A web page inside the tile. src follows home/away like the tile's link.
  // view: { height, zoom, reload, interactive, blocked, hint } from the server.
  // fill: in the free grid the tile's size sets the height.
  let { src, view, fill = false, editMode = false, newTab = true } = $props();

  let scale = $derived((view?.zoom || 100) / 100);
  // Reloading swaps the frame for a new one.
  let round = $state(0);
  $effect(() => {
    if (!view?.reload) return;
    const timer = setInterval(() => document.visibilityState === 'visible' && round++, view.reload * 1000);
    return () => clearInterval(timer);
  });
</script>

{#if view?.blocked}
  <div class="blocked" class:fill>
    <span>🚫 {t('Diese Seite lässt sich hier nicht anzeigen')}</span>
    <span class="why">{view.blocked}</span>
    {#if src}<a class="button small" href={src} target={newTab ? '_blank' : undefined} rel="noopener noreferrer">↗ {t('In neuem Tab öffnen')}</a>{/if}
  </div>
{:else if src}
  {#if view?.hint}<div class="hint">{view.hint}</div>{/if}
  <div class="frame" class:fill class:still={!view?.interactive || editMode} style:height={fill ? null : `${view?.height || 320}px`}>
    {#key round}
      <!-- No allow-top-navigation: the page can never lead Heimdeck itself away. -->
      <iframe {src} title={src} loading="lazy" referrerpolicy="no-referrer"
        sandbox="allow-scripts allow-same-origin allow-forms allow-popups allow-popups-to-escape-sandbox allow-downloads"
        style:width={`${100 / scale}%`} style:height={`${100 / scale}%`} style:transform={scale !== 1 ? `scale(${scale})` : null}></iframe>
    {/key}
  </div>
{/if}

<style>
  .frame { position: relative; overflow: hidden; border-radius: var(--r-sm); background: #fff; border: 1px solid var(--border); }
  .frame.fill { flex: 1; min-height: 0; }
  iframe { position: absolute; left: 0; top: 0; border: 0; transform-origin: 0 0; display: block; }
  /* View only (or while arranging tiles): the page gets no clicks or scrolling. */
  .frame.still iframe { pointer-events: none; }
  .blocked { display: grid; gap: 6px; justify-items: start; font-size: 13px; padding: 12px; border: 1px dashed var(--border-strong); border-radius: var(--r-sm); }
  .blocked.fill { flex: 1; align-content: center; }
  .why { color: var(--muted); font-size: 12.5px; }
  .hint { font-size: 12px; color: var(--muted); margin-bottom: 6px; }
</style>
