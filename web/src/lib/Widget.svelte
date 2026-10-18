<script>
  import { t } from './i18n.svelte.js';
  // Renders the generic widget format every integration returns. Long content
  // is folded: one warning (two lines) and three list entries, with an arrow
  // to show the rest.
  let { widget, onAction = null, newTab = true, link = (u) => u, compact = true } = $props();
  let busy = $state('');
  let expanded = $state(false);

  const MAX_ITEMS = 3;
  const LONG_ALERT = 110;

  let alerts = $derived(widget?.alerts || []);
  let items = $derived(widget?.items || []);
  let hiddenAlerts = $derived(compact && !expanded ? Math.max(0, alerts.length - 1) : 0);
  let hiddenItems = $derived(compact && !expanded ? Math.max(0, items.length - MAX_ITEMS) : 0);
  let foldable = $derived(compact && (alerts.length > 1 || items.length > MAX_ITEMS || alerts.some((a) => a.length > LONG_ALERT)));

  async function run(a) {
    if (!onAction) return;
    if (a.confirm && !confirm(a.confirm)) return;
    busy = keyOf(a);
    try {
      await onAction(a);
    } finally {
      busy = '';
    }
  }
  const keyOf = (a) => a.id + JSON.stringify(a.params || {});
</script>

{#if widget}
  <div class="widget">
    {#if widget.stats?.length}
      <div class="stats">
        {#each widget.stats as s}
          <div class="stat" title="{s.label}: {s.value}">
            <div class="value {s.tone || ''}">{s.value}</div>
            <div class="label">{s.label}</div>
          </div>
        {/each}
      </div>
    {/if}

    {#if alerts.length}
      <ul class="alerts" class:clamped={compact && !expanded}>
        {#each alerts.slice(0, alerts.length - hiddenAlerts) as a}<li title={a}>⚠ {a}</li>{/each}
      </ul>
    {/if}

    {#if items.length}
      <div class="items">
        {#if widget.itemsTitle}<div class="items-title">{widget.itemsTitle}</div>{/if}
        {#each items.slice(0, items.length - hiddenItems) as it}
          <div class="item">
            <div class="item-main">
              <div class="item-head">
                {#if it.url}
                  <a class="item-title" href={link(it.url)} target={newTab ? '_blank' : undefined} rel="noopener noreferrer" title={it.title}>{it.title}</a>
                {:else}
                  <span class="item-title" title={it.title}>{it.title}</span>
                {/if}
                {#if it.badge}<span class="badge">{it.badge}</span>{/if}
              </div>
              {#if it.subtitle}<div class="item-sub" title={it.subtitle}>{it.subtitle}</div>{/if}
              {#if it.progress != null}
                <div class="bar"><div style="width:{it.progress}%"></div></div>
              {/if}
            </div>
            {#if it.actions?.length && onAction}
              <div class="item-actions">
                {#each it.actions as a}
                  <button class="small {a.tone || ''}" disabled={busy === keyOf(a)} onclick={() => run(a)}>{a.label}</button>
                {/each}
              </div>
            {/if}
          </div>
        {/each}
      </div>
    {/if}

    {#if foldable}
      <button class="fold" onclick={() => (expanded = !expanded)} aria-expanded={expanded}>
        {#if expanded}▴ {t('weniger anzeigen')}{:else}▾ {hiddenItems + hiddenAlerts > 0 ? t('{n} weitere anzeigen', { n: hiddenItems + hiddenAlerts }) : t('alles anzeigen')}{/if}
      </button>
    {/if}

    {#if widget.actions?.length && onAction}
      <div class="row">
        {#each widget.actions as a}
          <button class="small {a.tone || ''}" disabled={busy === keyOf(a)} onclick={() => run(a)}>{a.label}</button>
        {/each}
      </div>
    {/if}
  </div>
{/if}

<style>
  /* minmax(0, 1fr) keeps long, unbreakable text from widening the tile. */
  .widget { display: grid; grid-template-columns: minmax(0, 1fr); gap: 10px; min-width: 0; }
  .stats { display: grid; grid-template-columns: repeat(auto-fit, minmax(88px, 1fr)); gap: 6px; }
  .stat { background: var(--surface-2); border-radius: 10px; padding: 8px 10px; min-width: 0; }
  .value { font-size: 17px; font-weight: 650; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; font-variant-numeric: tabular-nums; }
  .label { font-size: 11.5px; color: var(--muted); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .alerts { margin: 0; padding: 8px 10px; list-style: none; font-size: 12.5px; line-height: 1.4; color: var(--warn); background: color-mix(in srgb, var(--warn) 10%, transparent); border-radius: 10px; display: grid; gap: 4px; min-width: 0; overflow-wrap: anywhere; }
  .alerts.clamped li { display: -webkit-box; -webkit-line-clamp: 2; line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden; }
  .items { display: grid; grid-template-columns: minmax(0, 1fr); gap: 2px; min-width: 0; }
  .items-title { font-size: 11.5px; text-transform: uppercase; letter-spacing: .06em; color: var(--muted); margin-bottom: 4px; }
  .item { display: flex; flex-wrap: wrap; gap: 6px 8px; align-items: center; padding: 6px 0; border-top: 1px solid var(--border); min-width: 0; }
  .item:first-of-type { border-top: 0; }
  .item-main { flex: 1 1 160px; min-width: 0; }
  .item-head { display: flex; gap: 6px; align-items: center; min-width: 0; }
  .item-title { font-size: 13.5px; font-weight: 500; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; min-width: 0; }
  a.item-title:hover { color: var(--accent); }
  .item-sub { font-size: 12px; color: var(--muted); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .badge { font-size: 10.5px; padding: 1px 6px; border-radius: 6px; background: var(--surface-2); color: var(--muted); white-space: nowrap; flex: none; }
  .bar { height: 4px; background: var(--surface-2); border-radius: 4px; margin-top: 5px; overflow: hidden; }
  .bar > div { height: 100%; background: var(--accent); border-radius: 4px; transition: width .4s; }
  .item-actions { display: flex; gap: 4px; flex: none; margin-left: auto; }
  .fold { justify-self: start; background: none; border: 0; padding: 0; font-size: 12.5px; color: var(--muted); }
  .fold:hover { color: var(--accent); }
</style>
