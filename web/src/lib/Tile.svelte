<script module>
  // Live data of all tiles shares at most four connections, so the browser
  // (six connections per server) always has some left for clicks and saving.
  let active = 0;
  const waiting = [];
  function limited(fn) {
    return new Promise((resolve, reject) => {
      const run = () => {
        active++;
        fn().then(resolve, reject).finally(() => {
          active--;
          waiting.shift()?.();
        });
      };
      if (active < 4) run();
      else waiting.push(run);
    });
  }
</script>

<script>
  import { get, post, faviconUrl } from './api.js';
  import { toast } from './toast.svelte.js';
  import Icon from './Icon.svelte';
  import Widget from './Widget.svelte';
  import { tileHref, rewrite } from './network.svelte.js';
  import { t, i18n } from './i18n.svelte.js';

  // free: the tile sits in a free grid, whose size the user sets, so long content is not folded.
  let { tile, def, status, editMode, newTab, local, uniform = false, compact = '', free = false, onEdit, dragProps } = $props();

  let widget = $state(null);
  let error = $state('');
  let loaded = $state(false);

  let missing = $derived(
    (def?.fields || []).filter((f) => f.required && (f.secret ? !tile.secretsSet.includes(f.key) : !tile.config?.[f.key])),
  );
  // Compact tiles (icon only) never load live data.
  let live = $derived(!compact && def?.hasWidget && tile.showWidget && missing.length === 0);

  // Never more than one request per tile; the server answers right away and
  // says "pending" while a slow service is still being asked.
  let busy = false;
  let again = false; // a load was requested while one was running (e.g. after an action)
  let retry;
  async function load() {
    if (busy) {
      again = true;
      return;
    }
    busy = true;
    clearTimeout(retry);
    try {
      const res = await limited(() => get(`/tiles/${tile.id}/widget`));
      if (res.pending) {
        retry = setTimeout(load, 2500);
      } else {
        widget = res.widget;
        error = res.error || '';
        loaded = true;
      }
    } catch (e) {
      error = e.message;
      loaded = true;
    } finally {
      busy = false;
      if (again) {
        again = false;
        load();
      }
    }
  }

  $effect(() => {
    if (!live) return;
    tile.id; def?.refresh; tile.apiUrl; tile.url; tile.integration; tile.config; tile.secretsSet; i18n.lang; // reload when the tile or language changes
    loaded = false;
    load();
    const timer = setInterval(() => document.visibilityState === 'visible' && load(), (def?.refresh || 15) * 1000);
    return () => { clearInterval(timer); clearTimeout(retry); };
  });

  async function action(a) {
    try {
      await post(`/tiles/${tile.id}/action`, { action: a.id, params: a.params || {} });
      toast(t('{action}: erledigt', { action: a.label }));
      await load();
    } catch (e) {
      toast(e.message, 'bad');
    }
  }

  // Green: reachable. Yellow: reachable at home, but the public address is down. Red: down.
  let dot = $derived.by(() => {
    if ((!tile.url && !tile.apiUrl) || tile.monitor === false) return '';
    if (status == null) return 'unknown';
    if (!status.online) return 'down';
    return status.ext && !status.ext.online ? 'partial' : 'up';
  });
  let dotTitle = $derived.by(() => {
    if (dot === 'unknown') return t('Wird geprüft …');
    const line = (label, c) => `${label}: ${c.online ? `online · ${c.ms} ms` : t('nicht erreichbar')}`;
    const parts = [line(status.ext ? t('Lokal') : t('Status'), status)];
    if (status.ext) parts.push(line(t('Öffentlich'), status.ext));
    return parts.join('\n');
  });
  let href = $derived(tileHref(tile, local));
</script>

{#if compact}
<div class="tile card compact {compact}" class:editing={editMode} {...dragProps}>
  <a class="square" href={editMode ? undefined : href || undefined} target={newTab ? '_blank' : undefined} rel="noopener noreferrer"
     title={tile.description ? `${tile.name} – ${tile.description}` : tile.name} aria-label={tile.name}
     onclick={(e) => { if (editMode) { e.preventDefault(); onEdit(); } }}>
    <Icon icon={tile.icon} name={tile.name} favicon={faviconUrl(tile)} size={44} />
    <span class="cname">{tile.name}</span>
  </a>
  {#if dot}<span class="dot corner {dot}" title={dotTitle}></span>{/if}
  {#if editMode}
    <button class="ghost icon edit cedit" onclick={(e) => { e.preventDefault(); e.stopPropagation(); onEdit(); }} aria-label={t('Bearbeiten')}>✎</button>
  {/if}
</div>
{:else}
<div class="tile card" class:wide={!uniform && (live || (missing.length > 0 && def?.hasWidget && tile.showWidget))} class:uniform class:editing={editMode} {...dragProps}>
  <a class="head" href={editMode ? undefined : href || undefined} target={newTab ? '_blank' : undefined} rel="noopener noreferrer"
     onclick={(e) => { if (editMode) { e.preventDefault(); onEdit(); } }}>
    <Icon icon={tile.icon} name={tile.name} favicon={faviconUrl(tile)} size={live ? 34 : 40} />
    <div class="titles">
      <div class="name">{tile.name}</div>
      {#if tile.description}<div class="desc">{tile.description}</div>{/if}
    </div>
    {#if dot}
      <span class="dot {dot}" title={dotTitle}></span>
    {/if}
    {#if editMode}
      <button class="ghost icon edit" onclick={(e) => { e.preventDefault(); e.stopPropagation(); onEdit(); }} aria-label={t('Bearbeiten')}>✎</button>
    {/if}
  </a>

  {#if def?.hasWidget && tile.showWidget && missing.length > 0}
    <button class="setup" onclick={onEdit}>🔑 {t('{fields} fehlt – jetzt einrichten', { fields: missing.map((f) => f.label).join(', ') })}</button>
  {:else if live}
    <div class="body">
      {#if error}
        <div class="error">{error}</div>
      {:else if !loaded}
        <div class="skeleton"></div>
      {:else}
        <Widget {widget} onAction={action} {newTab} link={(u) => rewrite(u, tile, local)} compact={!free} />
      {/if}
    </div>
  {/if}
</div>
{/if}

<style>
  /* translateZ gives each tile its own layer: Chrome sometimes skips repainting text inside
     backdrop-filter elements until hover, which made tile names flicker in on load. */
  .tile { display: flex; flex-direction: column; min-width: 0; transform: translateZ(0); transition: transform .15s, border-color .15s; }
  .tile.wide { grid-column: span 2; }
  .tile:hover { border-color: var(--border-strong); }
  .tile.editing { cursor: grab; outline: 1px dashed var(--border-strong); outline-offset: 2px; }
  .head { display: flex; align-items: center; gap: 12px; padding: 14px; min-width: 0; }
  .tile:not(.editing) .head[href]:hover .name { color: var(--accent); }
  .titles { flex: 1; min-width: 0; }
  .name { font-weight: 600; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .desc { font-size: 12.5px; color: var(--muted); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .dot { width: 9px; height: 9px; border-radius: 50%; flex: none; background: var(--muted); }
  .dot.up { background: var(--ok); box-shadow: 0 0 0 3px color-mix(in srgb, var(--ok) 22%, transparent); }
  .dot.down { background: var(--bad); box-shadow: 0 0 0 3px color-mix(in srgb, var(--bad) 22%, transparent); }
  .dot.partial { background: var(--warn); box-shadow: 0 0 0 3px color-mix(in srgb, var(--warn) 22%, transparent); }
  .dot.unknown { opacity: .4; }
  .edit { flex: none; }
  .body { padding: 0 14px 14px; min-width: 0; }
  .error { font-size: 12.5px; color: var(--bad); background: color-mix(in srgb, var(--bad) 10%, transparent); padding: 8px 10px; border-radius: 10px; }
  .skeleton { height: 54px; border-radius: 10px; background: linear-gradient(90deg, var(--surface-2), var(--border), var(--surface-2)); background-size: 200% 100%; animation: shimmer 1.2s infinite; }
  .setup { margin: 0 14px 14px; font-size: 13px; text-align: left; border-style: dashed; }
  @keyframes shimmer { to { background-position: -200% 0; } }

  /* Compact: a square with the icon; the name below or, with icons-hover, on hover. */
  /* The width comes from the column; without it the square would take its width
     from a taller row and overlap the next tile (seen on small iPhones). */
  .tile.compact { position: relative; aspect-ratio: 1; width: 100%; }
  .square { flex: 1; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 8px; padding: 12px 8px; min-width: 0; min-height: 0; }
  .cname { font-size: 12px; font-weight: 500; max-width: 100%; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; color: var(--muted); }
  .tile.compact:not(.editing) .square[href]:hover .cname { color: var(--accent); }
  .icons-hover .cname {
    position: absolute; left: 6px; right: 6px; bottom: 8px; text-align: center; opacity: 0; transform: translateY(4px);
    transition: opacity .15s, transform .15s; pointer-events: none; color: var(--text);
  }
  .icons-hover:hover .cname, .icons-hover .square:focus-visible .cname, .icons-hover.editing .cname { opacity: 1; transform: none; }
  /* Touch screens have no hover: always show the name there. */
  @media (hover: none) { .icons-hover .cname { opacity: 1; transform: none; } }
  .corner { position: absolute; top: 9px; right: 9px; width: 8px; height: 8px; }
  .cedit { position: absolute; top: 2px; left: 2px; width: 28px; height: 28px; padding: 0; justify-content: center; }
  @media (max-width: 520px) { .tile.wide { grid-column: 1 / -1; } }
</style>
