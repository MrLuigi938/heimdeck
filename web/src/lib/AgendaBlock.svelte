<script>
  import { t } from './i18n.svelte.js';

  // The next dates from a calendar tile, as many as fit completely.
  let { widget, shape } = $props();

  let items = $derived(widget?.items || []);
  let listH = $state(0);
  let rowH = $derived(shape === 'mini' || shape === 'small' || shape === 'band' ? 22 : 40);
  let rows = $derived(Math.max(1, Math.floor((listH + 4) / rowH)));
  let today = $derived((widget?.stats || []).find((s) => s.label === t('Heute'))?.value);
</script>

<div class="agenda shape-{shape}">
  {#if shape !== 'mini'}
    <div class="head"><span>{widget?.itemsTitle || t('Termine')}</span>{#if today != null && today !== '0'}<span class="today">{t('heute {n}', { n: today })}</span>{/if}</div>
  {/if}
  <div class="list" bind:clientHeight={listH}>
    {#each items.slice(0, rows) as e, i (i)}
      <div class="ev" class:soon={!!e.badge}>
        <span class="bar" aria-hidden="true"></span>
        <span class="txt">
          <b>{e.title}</b>
          {#if rowH > 22 && e.subtitle}<span class="when">{e.subtitle}</span>{/if}
        </span>
        {#if e.badge && shape !== 'mini'}<span class="badge">{e.badge}</span>{/if}
      </div>
    {:else}
      <div class="empty">{t('Keine Termine')}</div>
    {/each}
  </div>
</div>

<style>
  .agenda { height: 100%; display: flex; flex-direction: column; gap: .4em; padding: clamp(8px, 4cqmin, 16px); box-sizing: border-box; overflow: hidden; text-align: left;
    font-size: calc(clamp(11px, min(4cqw, 7cqh), 15px) * var(--ib-scale, 1)); }
  .head { display: flex; align-items: baseline; gap: .6em; font-size: .78em; text-transform: uppercase; letter-spacing: .05em; opacity: .7; font-weight: 650; flex: none; white-space: nowrap; overflow: hidden; }
  .today { margin-left: auto; color: var(--ib-accent); opacity: 1; }
  .list { flex: 1 1 0; min-height: 0; overflow: hidden; display: flex; flex-direction: column; }
  .ev { display: flex; align-items: center; gap: .6em; height: 40px; flex: none; min-width: 0; }
  .shape-mini .ev, .shape-small .ev, .shape-band .ev { height: 22px; }
  .bar { width: 3px; align-self: stretch; margin: 5px 0; border-radius: 2px; background: color-mix(in srgb, var(--ib-accent) 45%, transparent); flex: none; }
  .soon .bar { background: var(--ib-accent); }
  .txt { display: flex; flex-direction: column; min-width: 0; line-height: 1.25; flex: 1; }
  .txt b, .when { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .txt b { font-weight: 600; }
  .when { font-size: .86em; opacity: .65; }
  .badge { font-size: .78em; padding: 1px 7px; border-radius: 999px; background: color-mix(in srgb, var(--ib-accent) 18%, transparent); color: var(--ib-accent); flex: none; font-weight: 600; }
  .empty { opacity: .6; }
</style>
