<script>
  import { t } from './i18n.svelte.js';

  // Is everything running? "All good – 38 services reachable" or which ones
  // are down; a click on one jumps to its group.
  let { tiles = [], status = {}, groups = [], shape, onJump } = $props();

  let checked = $derived(tiles.filter((x) => status[x.id]));
  let down = $derived(checked.filter((x) => !status[x.id].online || (status[x.id].ext && !status[x.id].ext.online)));
  let groupOf = $derived(Object.fromEntries(groups.flatMap((g) => g.tiles.map((x) => [x.id, g.id]))));
  let ok = $derived(down.length === 0);

  // Only the names that fit fully.
  let listH = $state(0);
  let rows = $derived(Math.max(0, Math.floor((listH + 2) / 22)));
</script>

<div class="status shape-{shape}" class:bad={!ok} class:none={!checked.length}>
  <div class="head">
    <span class="dot" aria-hidden="true"></span>
    {#if !checked.length}
      <span class="what" title={t('Noch keine Statusprüfung')}>{shape === 'mini' ? '–' : t('Noch keine Statusprüfung')}</span>
    {:else if shape === 'mini'}
      <span class="what">{ok ? t('alles ok') : down.length}</span>
    {:else if ok}
      <span class="what">{t('Alles in Ordnung')}</span>
    {:else}
      <span class="what">{down.length === 1 ? t('1 Dienst gestört') : t('{n} Dienste gestört', { n: down.length })}</span>
    {/if}
  </div>
  {#if shape !== 'mini' && checked.length}
    {#if ok}
      <div class="sub">{checked.length === 1 ? t('1 Dienst erreichbar') : t('{n} Dienste erreichbar', { n: checked.length })}</div>
    {:else}
      <div class="list" bind:clientHeight={listH}>
        {#each down.slice(0, down.length > rows ? Math.max(0, rows - 1) : rows) as x (x.id)}
          <button type="button" class="svc" onclick={() => groupOf[x.id] && onJump?.(groupOf[x.id])} title={t('Zur Kachel springen')}>
            <b>{x.name}</b>{#if status[x.id].online && status[x.id].ext}<span> · {t('öffentlich nicht erreichbar')}</span>{/if}
          </button>
        {/each}
        {#if down.length > rows && rows > 0}<span class="more">{t('und {n} weitere', { n: down.length - rows + 1 })}</span>{/if}
      </div>
    {/if}
  {/if}
</div>

<style>
  .status { --ok: var(--good, #3ccf7a); --bad: var(--danger, #ff5d5d); height: 100%; display: flex; flex-direction: column; justify-content: center; gap: .35em;
    padding: clamp(6px, 4cqmin, 16px) clamp(8px, 5cqmin, 18px); box-sizing: border-box; overflow: hidden; text-align: left;
    font-size: calc(clamp(11px, min(4.6cqw, 9cqh), 16px) * var(--ib-scale, 1)); }
  .head { display: flex; align-items: center; gap: .6em; font-weight: 650; font-size: calc(clamp(12px, min(7cqw, 20cqh), 30px) * var(--ib-scale, 1)); min-width: 0; flex: none; justify-content: var(--ib-items, flex-start); }
  .shape-mini .head { justify-content: center; font-size: calc(clamp(11px, min(14cqw, 38cqh), 26px) * var(--ib-scale, 1)); }
  .what { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .dot { width: .6em; height: .6em; border-radius: 50%; flex: none; background: var(--ok); box-shadow: 0 0 0 .22em color-mix(in srgb, var(--ok) 22%, transparent); }
  .bad .dot { background: var(--bad); box-shadow: 0 0 0 .22em color-mix(in srgb, var(--bad) 25%, transparent); animation: st-pulse 2s ease-in-out infinite; }
  .none .dot { background: var(--muted); box-shadow: none; }
  @keyframes st-pulse { 50% { box-shadow: 0 0 0 .4em color-mix(in srgb, var(--bad) 8%, transparent); } }
  @media (prefers-reduced-motion: reduce) { .bad .dot { animation: none; } }
  .sub { opacity: .7; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  /* The list takes the room below the headline whatever it holds: how many
     names fit is measured on it, so it must not size itself by its names
     (it would stay empty – no names, no height, no names). */
  .list { display: flex; flex-direction: column; flex: 1 1 0; min-height: 0; overflow: hidden; }
  .bad { justify-content: flex-start; }
  .svc { all: unset; cursor: pointer; height: 22px; line-height: 22px; flex: none; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; opacity: .85; }
  .svc:hover { opacity: 1; text-decoration: underline; }
  .svc:focus-visible { outline: 2px solid var(--ib-accent); border-radius: 4px; }
  .svc span, .more { opacity: .7; font-size: .92em; }
  .more { height: 22px; line-height: 22px; flex: none; }
</style>
