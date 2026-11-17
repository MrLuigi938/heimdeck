<script>
  import { t, locale } from './i18n.svelte.js';

  // Days until a date – a holiday, a birthday (yearly) – with a bar since the
  // start date when one is given.
  let { opt, shape } = $props();

  let today = $state(new Date());
  $effect(() => {
    const timer = setInterval(() => (today = new Date()), 60000);
    return () => clearInterval(timer);
  });
  const day = (d) => new Date(d.getFullYear(), d.getMonth(), d.getDate());
  const parse = (s) => {
    const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(s || '');
    return m ? new Date(+m[1], +m[2] - 1, +m[3]) : null;
  };
  let target = $derived.by(() => {
    let d = parse(opt.date);
    if (!d) return null;
    if (opt.yearly) {
      d = new Date(today.getFullYear(), d.getMonth(), d.getDate());
      if (d < day(today)) d.setFullYear(d.getFullYear() + 1);
    }
    return d;
  });
  let days = $derived(target ? Math.round((target - day(today)) / 86400e3) : null);
  let progress = $derived.by(() => {
    const s = parse(opt.start);
    if (!s || !target || target <= s) return null;
    return Math.max(0, Math.min(1, (day(today) - s) / (target - s)));
  });
  let label = $derived(opt.label || (target ? target.toLocaleDateString(locale(), { day: 'numeric', month: 'long' }) : ''));
</script>

<div class="cd shape-{shape}">
  {#if days == null}
    <span class="empty">{t('Datum in den Block-Einstellungen wählen')}</span>
  {:else}
    <div class="num">{days === 0 ? '🎉' : Math.abs(days)}</div>
    {#if shape !== 'mini'}
      <div class="lbl">
        {#if days === 0}{t('Heute: {what}', { what: label })}
        {:else if days > 0}{days === 1 ? t('Tag bis {what}', { what: label }) : t('Tage bis {what}', { what: label })}
        {:else}{t('Tage seit {what}', { what: label })}{/if}
      </div>
      {#if progress != null && shape !== 'small'}<div class="bar"><i style:width="{progress * 100}%"></i></div>{/if}
    {/if}
  {/if}
</div>

<style>
  .cd { height: 100%; display: flex; flex-direction: column; align-items: var(--ib-items, center); justify-content: center; text-align: var(--ib-align, center);
    padding: clamp(6px, 4cqmin, 16px); box-sizing: border-box; overflow: hidden; gap: .25em; }
  .num { font-size: calc(min(30cqw, 52cqh) * var(--ib-scale, 1)); font-weight: 750; line-height: 1; color: var(--ib-accent); font-variant-numeric: tabular-nums; }
  .shape-mini { flex-direction: row; }
  .shape-mini .num { font-size: calc(min(30cqw, 70cqh) * var(--ib-scale, 1)); }
  .lbl { font-size: calc(clamp(11px, min(5.5cqw, 11cqh), 22px) * var(--ib-scale, 1)); opacity: .8; max-width: 100%; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .bar { width: 80%; height: max(4px, 3cqh); max-height: 8px; border-radius: 4px; background: color-mix(in srgb, currentColor 12%, transparent); overflow: hidden; margin-top: .3em; flex: none; }
  .bar i { display: block; height: 100%; background: var(--ib-accent); border-radius: inherit; }
  .empty { opacity: .55; font-size: 13px; }
</style>
