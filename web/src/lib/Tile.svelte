<script>
  import { get, post } from './api.js';
  import { toast } from './toast.svelte.js';
  import Icon from './Icon.svelte';
  import Widget from './Widget.svelte';

  let { tile, def, status, editMode, newTab, onEdit, dragProps } = $props();

  let widget = $state(null);
  let error = $state('');
  let loaded = $state(false);

  let missing = $derived(
    (def?.fields || []).filter((f) => f.required && (f.secret ? !tile.secretsSet.includes(f.key) : !tile.config?.[f.key])),
  );
  let live = $derived(def?.hasWidget && tile.showWidget && missing.length === 0);

  async function load() {
    try {
      const res = await get(`/tiles/${tile.id}/widget`);
      widget = res.widget;
      error = res.error || '';
    } catch (e) {
      error = e.message;
    }
    loaded = true;
  }

  $effect(() => {
    if (!live) return;
    tile.id; tile.apiUrl; tile.url; tile.integration; // reload when the tile is edited
    loaded = false;
    load();
    const timer = setInterval(() => document.visibilityState === 'visible' && load(), 15000);
    return () => clearInterval(timer);
  });

  async function action(a) {
    try {
      await post(`/tiles/${tile.id}/action`, { action: a.id, params: a.params || {} });
      toast(`${a.label}: erledigt`);
      await load();
    } catch (e) {
      toast(e.message, 'bad');
    }
  }

  let dot = $derived(!tile.url && !tile.apiUrl ? '' : status == null ? 'unknown' : status.online ? 'up' : 'down');
</script>

<div class="tile card" class:wide={live || missing.length > 0 && def?.hasWidget && tile.showWidget} class:editing={editMode} {...dragProps}>
  <a class="head" href={editMode ? undefined : tile.url || undefined} target={newTab ? '_blank' : undefined} rel="noopener noreferrer"
     onclick={(e) => { if (editMode) { e.preventDefault(); onEdit(); } }}>
    <Icon icon={tile.icon} name={tile.name} size={live ? 34 : 40} />
    <div class="titles">
      <div class="name">{tile.name}</div>
      {#if tile.description}<div class="desc">{tile.description}</div>{/if}
    </div>
    {#if dot}
      <span class="dot {dot}" title={dot === 'up' ? `Online · ${status.ms} ms` : dot === 'down' ? 'Nicht erreichbar' : 'Prüfe …'}></span>
    {/if}
    {#if editMode}
      <button class="ghost icon edit" onclick={(e) => { e.preventDefault(); e.stopPropagation(); onEdit(); }} aria-label="Bearbeiten">✎</button>
    {/if}
  </a>

  {#if def?.hasWidget && tile.showWidget && missing.length > 0}
    <button class="setup" onclick={onEdit}>🔑 {missing.map((f) => f.label).join(', ')} fehlt – jetzt einrichten</button>
  {:else if live}
    <div class="body">
      {#if error}
        <div class="error">{error}</div>
      {:else if !loaded}
        <div class="skeleton"></div>
      {:else}
        <Widget {widget} onAction={action} {newTab} />
      {/if}
    </div>
  {/if}
</div>

<style>
  .tile { display: flex; flex-direction: column; min-width: 0; transition: transform .15s, border-color .15s; }
  .tile.wide { grid-column: span 2; }
  .tile:hover { border-color: var(--border-strong); }
  .tile.editing { cursor: grab; outline: 1px dashed var(--border-strong); outline-offset: 2px; }
  .head { display: flex; align-items: center; gap: 12px; padding: 14px; min-width: 0; }
  .tile:not(.editing) .head[href]:hover .name { color: var(--accent); }
  .titles { flex: 1; min-width: 0; }
  .name { font-weight: 600; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; transition: color .15s; }
  .desc { font-size: 12.5px; color: var(--muted); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .dot { width: 9px; height: 9px; border-radius: 50%; flex: none; background: var(--muted); }
  .dot.up { background: var(--ok); box-shadow: 0 0 0 3px color-mix(in srgb, var(--ok) 22%, transparent); }
  .dot.down { background: var(--bad); box-shadow: 0 0 0 3px color-mix(in srgb, var(--bad) 22%, transparent); }
  .dot.unknown { opacity: .4; }
  .edit { flex: none; }
  .body { padding: 0 14px 14px; }
  .error { font-size: 12.5px; color: var(--bad); background: color-mix(in srgb, var(--bad) 10%, transparent); padding: 8px 10px; border-radius: 10px; }
  .skeleton { height: 54px; border-radius: 10px; background: linear-gradient(90deg, var(--surface-2), var(--border), var(--surface-2)); background-size: 200% 100%; animation: shimmer 1.2s infinite; }
  .setup { margin: 0 14px 14px; font-size: 13px; text-align: left; border-style: dashed; }
  @keyframes shimmer { to { background-position: -200% 0; } }
  @media (max-width: 520px) { .tile.wide { grid-column: 1 / -1; } }
</style>
