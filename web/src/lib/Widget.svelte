<script>
  // Renders the generic widget format every integration returns.
  let { widget, onAction = null, newTab = true } = $props();
  let busy = $state('');

  async function run(a) {
    if (!onAction) return;
    if (a.confirm && !confirm(a.confirm)) return;
    const key = a.id + JSON.stringify(a.params || {});
    busy = key;
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
          <div class="stat">
            <div class="value {s.tone || ''}">{s.value}</div>
            <div class="label">{s.label}</div>
          </div>
        {/each}
      </div>
    {/if}

    {#if widget.alerts?.length}
      <ul class="alerts">
        {#each widget.alerts as a}<li>⚠ {a}</li>{/each}
      </ul>
    {/if}

    {#if widget.items?.length}
      <div class="items">
        {#if widget.itemsTitle}<div class="items-title">{widget.itemsTitle}</div>{/if}
        {#each widget.items as it}
          <div class="item">
            <div class="item-main">
              <div class="item-head">
                {#if it.url}
                  <a class="item-title" href={it.url} target={newTab ? '_blank' : undefined} rel="noopener noreferrer">{it.title}</a>
                {:else}
                  <span class="item-title">{it.title}</span>
                {/if}
                {#if it.badge}<span class="badge">{it.badge}</span>{/if}
              </div>
              {#if it.subtitle}<div class="item-sub">{it.subtitle}</div>{/if}
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
  .widget { display: grid; gap: 10px; }
  .stats { display: grid; grid-template-columns: repeat(auto-fit, minmax(88px, 1fr)); gap: 6px; }
  .stat { background: var(--surface-2); border-radius: 10px; padding: 8px 10px; min-width: 0; }
  .value { font-size: 17px; font-weight: 650; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; font-variant-numeric: tabular-nums; }
  .label { font-size: 11.5px; color: var(--muted); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .alerts { margin: 0; padding: 8px 10px; list-style: none; font-size: 12.5px; color: var(--warn); background: color-mix(in srgb, var(--warn) 10%, transparent); border-radius: 10px; display: grid; gap: 4px; }
  .items { display: grid; gap: 2px; }
  .items-title { font-size: 11.5px; text-transform: uppercase; letter-spacing: .06em; color: var(--muted); margin-bottom: 4px; }
  .item { display: flex; flex-wrap: wrap; gap: 6px 8px; align-items: center; padding: 6px 0; border-top: 1px solid var(--border); }
  .item:first-of-type { border-top: 0; }
  .item-main { flex: 1 1 160px; min-width: 0; }
  .item-head { display: flex; gap: 6px; align-items: center; min-width: 0; }
  .item-title { font-size: 13.5px; font-weight: 500; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  a.item-title:hover { color: var(--accent); }
  .item-sub { font-size: 12px; color: var(--muted); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .badge { font-size: 10.5px; padding: 1px 6px; border-radius: 6px; background: var(--surface-2); color: var(--muted); white-space: nowrap; flex: none; }
  .bar { height: 4px; background: var(--surface-2); border-radius: 4px; margin-top: 5px; overflow: hidden; }
  .bar > div { height: 100%; background: var(--accent); border-radius: 4px; transition: width .4s; }
  .item-actions { display: flex; gap: 4px; flex: none; margin-left: auto; }
</style>
