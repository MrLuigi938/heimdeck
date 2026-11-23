<script>
  import { t, locale } from './i18n.svelte.js';

  // A diagram Heimdeck draws itself – no Grafana, no chart library: an SVG
  // in the size of its box, in the theme's colours.
  //   line | area | stacked (bars over time) | bar (categories) | donut
  // chart: { title, kind, unit, bytes, step, times[], labels[], series[{name, values, color}], lines[{value, label, tone}], note }
  // The size decides how much it shows: small boxes leave out axes and legend.
  let { chart, height = 0, bare = false } = $props();

  let w = $state(0);
  let fw = $state(0); // the whole figure (bars and donut have no plot of their own)
  let h = $state(0);
  // A height of its own (in a tile), or the height of the box (in a block).
  let H = $derived(height || h || 140);
  const PALETTE = ['var(--accent)', '#eda100', '#e5484d', '#a855f7', '#22c55e', '#06b6d4', '#f97316', '#ec4899'];
  let series = $derived((chart?.series || []).map((s, i) => ({ ...s, color: s.color || PALETTE[i % PALETTE.length] })));
  let kind = $derived(chart?.kind || 'line');
  let isTime = $derived(kind === 'line' || kind === 'area' || kind === 'stacked');
  let n = $derived(isTime ? chart?.times?.length || series[0]?.values?.length || 0 : chart?.labels?.length || 0);
  // How much room there is: tiny = only the shape, small = without axes.
  let room = $derived(kind === 'bar' || kind === 'donut' ? fw : w);
  let tiny = $derived(room < 160 || (kind !== 'bar' && kind !== 'donut' && H < 70));
  let small = $derived(!tiny && (room < 260 || H < 96));

  // ---- numbers ----
  const nf = (max = 1) => new Intl.NumberFormat(locale(), { maximumFractionDigits: max });
  function bytes(v) {
    const u = ['B', 'KB', 'MB', 'GB', 'TB'];
    let i = 0;
    while (Math.abs(v) >= 1024 && i < u.length - 1) { v /= 1024; i++; }
    return `${nf(v < 10 && i ? 1 : 0).format(v)} ${u[i]}`;
  }
  const fmt = (v) => (v == null || Number.isNaN(v) ? '–' : chart.bytes ? bytes(v) + (chart.unit || '') : `${nf(Math.abs(v) < 10 ? 1 : 0).format(v)}${chart.unit ? (chart.unit === '%' ? ' %' : ' ' + chart.unit) : ''}`);
  const short = (v) => (chart.bytes ? bytes(v) : Math.abs(v) >= 1e6 ? nf(1).format(v / 1e6) + ' M' : Math.abs(v) >= 1e4 ? nf(0).format(v / 1e3) + ' k' : nf(Math.abs(v) < 10 ? 1 : 0).format(v));
  function timeLabel(ms, withDay) {
    const d = new Date(ms);
    const span = (chart.times?.at(-1) || 0) - (chart.times?.[0] || 0);
    if (span > 3 * 86400e3 && !withDay) return d.toLocaleDateString(locale(), { day: 'numeric', month: 'numeric' });
    const time = d.toLocaleTimeString(locale(), { hour: '2-digit', minute: '2-digit' });
    return withDay ? `${d.toLocaleDateString(locale(), { weekday: 'short', day: 'numeric', month: 'numeric' })} ${time}` : time;
  }

  // ---- the plot area ----
  let pad = $derived({ l: tiny || small ? 2 : 40, r: 4, t: 6, b: tiny || small ? 2 : 18 });
  let pw = $derived(Math.max(10, w - pad.l - pad.r));
  let ph = $derived(Math.max(10, H - pad.t - pad.b));
  // The top of the scale: the largest value (stacked: the largest sum), rounded up nicely.
  let top = $derived.by(() => {
    let m = 0;
    if (kind === 'stacked') for (let i = 0; i < n; i++) m = Math.max(m, series.reduce((a, s) => a + (s.values[i] || 0), 0));
    else for (const s of series) for (const v of s.values) m = Math.max(m, v || 0);
    for (const l of chart?.lines || []) m = Math.max(m, l.value);
    if (m <= 0) return 1;
    const p = 10 ** Math.floor(Math.log10(m));
    return [1, 2, 2.5, 5, 10].map((f) => f * p).find((x) => x >= m) || m;
  });
  const X = (i) => pad.l + (n <= 1 ? pw / 2 : (i / (n - 1)) * pw);
  const Y = (v) => pad.t + ph - (Math.max(0, v) / top) * ph;
  let ticks = $derived(small || tiny ? [] : [0, top / 2, top]);
  // Time labels: as many as fit.
  let xLabels = $derived.by(() => {
    if (!isTime || small || tiny || !chart.times?.length) return [];
    const every = Math.max(1, Math.ceil(n / Math.max(2, Math.floor(pw / 64))));
    const out = [];
    for (let i = 0; i < n; i += every) out.push(i);
    return out;
  });

  function path(values) {
    let d = '';
    values.forEach((v, i) => (d += `${i ? 'L' : 'M'}${X(i).toFixed(1)},${Y(v || 0).toFixed(1)}`));
    return d;
  }
  // Bars of a time series: each bucket one column, the series stacked.
  let barW = $derived(n ? Math.max(1, (pw / n) * 0.72) : 0);
  const BX = (i) => pad.l + (pw / n) * (i + 0.5);

  // ---- donut ----
  let total = $derived(kind === 'donut' ? (series[0]?.values || []).reduce((a, v) => a + (v || 0), 0) : 0);
  let arcs = $derived.by(() => {
    if (kind !== 'donut' || !total) return [];
    let a = -Math.PI / 2;
    return (series[0].values || []).map((v, i) => {
      const da = ((v || 0) / total) * Math.PI * 2;
      const r = { a0: a, a1: a + da, color: PALETTE[i % PALETTE.length], label: chart.labels?.[i], v };
      a += da;
      return r;
    });
  });
  let R = $derived(Math.max(20, Math.min(w * 0.4, (height || 120) / 2) - 2));
  function arc(a0, a1, R, r) {
    const big = a1 - a0 > Math.PI ? 1 : 0;
    const p = (a, rad) => `${(Math.cos(a) * rad).toFixed(2)},${(Math.sin(a) * rad).toFixed(2)}`;
    if (a1 - a0 >= Math.PI * 2 - 1e-6) a1 -= 1e-4;
    return `M${p(a0, R)}A${R},${R} 0 ${big} 1 ${p(a1, R)}L${p(a1, r)}A${r},${r} 0 ${big} 0 ${p(a0, r)}Z`;
  }

  // ---- hover / tap: the values at one point ----
  let hover = $state(-1);
  function onMove(e) {
    if (!isTime || !n) return;
    const r = e.currentTarget.getBoundingClientRect();
    const x = e.clientX - r.left;
    const i = kind === 'stacked' ? Math.floor(((x - pad.l) / pw) * n) : Math.round(((x - pad.l) / pw) * (n - 1));
    hover = i >= 0 && i < n ? i : -1;
  }
  let tipX = $derived(hover < 0 ? 0 : kind === 'stacked' ? BX(hover) : X(hover));
</script>

<figure class="chart" class:bare class:tiny class:small bind:clientWidth={fw}>
  {#if chart.title && !tiny && !bare}<figcaption>{chart.title}</figcaption>{/if}
  {#if kind === 'bar'}
    <!-- Categories: one bar per line, the label in front, the value after. -->
    <div class="hbars" style:--n={n}>
      {#each chart.labels || [] as label, i (i)}
        {@const v = series[0]?.values?.[i] || 0}
        <div class="hbar" title="{label}: {fmt(v)}">
          <span class="hl">{label}</span>
          <span class="track"><i style:width="{(v / top) * 100}%" style:background={series[0]?.color}></i></span>
          <span class="hv">{short(v)}</span>
        </div>
      {/each}
    </div>
  {:else if kind === 'donut'}
    <div class="donut" bind:clientWidth={w}>
      <svg viewBox="{-R - 2} {-R - 2} {2 * R + 4} {2 * R + 4}" width={2 * R + 4} height={2 * R + 4} role="img" aria-label={chart.title}>
        {#each arcs as a, i (i)}<path d={arc(a.a0, a.a1, R, R * 0.62)} fill={a.color}><title>{a.label}: {fmt(a.v)}</title></path>{/each}
        <text class="mid" y="0.35em" text-anchor="middle">{short(total)}</text>
      </svg>
      {#if !tiny}
        <ul class="legend">
          {#each arcs as a, i (i)}<li><i style:background={a.color}></i>{a.label} <b>{Math.round((a.v / total) * 100)} %</b></li>{/each}
        </ul>
      {/if}
    </div>
  {:else}
    <div class="plot" bind:clientWidth={w} bind:clientHeight={h} style:height={height ? `${height}px` : null}>
      {#if w}
        <svg width={w} height={H} role="img" aria-label={chart.title}
          onpointermove={onMove} onpointerleave={() => (hover = -1)} onpointerdown={onMove}>
          {#each ticks as tk (tk)}
            <line class="grid" x1={pad.l} x2={w - pad.r} y1={Y(tk)} y2={Y(tk)} />
            <text class="axis" x={pad.l - 6} y={Y(tk)} dy="0.32em" text-anchor="end">{short(tk)}</text>
          {/each}
          {#each xLabels as i (i)}
            <text class="axis" x={kind === 'stacked' ? BX(i) : X(i)} y={H - 4} text-anchor={i === 0 && kind !== 'stacked' ? 'start' : (kind === 'stacked' ? BX(i) : X(i)) > w - pad.r - 24 ? 'end' : 'middle'}>{timeLabel(chart.times[i])}</text>
          {/each}
          {#if kind === 'stacked'}
            {#each { length: n }, i (i)}
              {@const parts = series.map((s) => s.values[i] || 0)}
              {#each parts as v, k (k)}
                {@const below = parts.slice(0, k).reduce((a, x) => a + x, 0)}
                {#if v > 0}<rect x={BX(i) - barW / 2} y={Y(below + v)} width={barW} height={Math.max(0.5, Y(below) - Y(below + v))} fill={series[k].color} rx={barW > 6 ? 1.5 : 0} opacity={hover >= 0 && hover !== i ? 0.55 : 1} />{/if}
              {/each}
            {/each}
          {:else}
            {#each series as s, k (k)}
              {#if kind === 'area'}<path d="{path(s.values)}L{X(n - 1)},{Y(0)}L{X(0)},{Y(0)}Z" fill={s.color} opacity="0.16" />{/if}
              <path d={path(s.values)} fill="none" stroke={s.color} stroke-width={tiny ? 1.6 : 2} stroke-linejoin="round" stroke-linecap="round" />
            {/each}
          {/if}
          {#each chart.lines || [] as l, i (i)}
            <line class="limit {l.tone || ''}" x1={pad.l} x2={w - pad.r} y1={Y(l.value)} y2={Y(l.value)}><title>{l.label || fmt(l.value)}</title></line>
          {/each}
          {#if hover >= 0}
            <line class="cursor" x1={tipX} x2={tipX} y1={pad.t} y2={pad.t + ph} />
            {#if kind !== 'stacked'}{#each series as s, k (k)}<circle cx={X(hover)} cy={Y(s.values[hover] || 0)} r="3.5" fill={s.color} />{/each}{/if}
          {/if}
        </svg>
        {#if hover >= 0}
          <div class="tip" style:left="{Math.min(Math.max(tipX, 70), w - 70)}px">
            {#if chart.times?.[hover]}<div class="tt">{timeLabel(chart.times[hover], true)}</div>{/if}
            {#each series as s, k (k)}<div><i style:background={s.color}></i>{s.name} <b>{fmt(s.values[hover])}</b></div>{/each}
          </div>
        {/if}
      {/if}
    </div>
    {#if series.length > 1 && !tiny && !small}
      <ul class="legend row">
        {#each series as s, k (k)}<li><i style:background={s.color}></i>{s.name}</li>{/each}
      </ul>
    {/if}
  {/if}
  {#if chart.note && !tiny}<div class="note">{chart.note}</div>{/if}
</figure>

<style>
  .chart { margin: 0; display: flex; flex-direction: column; gap: 4px; min-width: 0; min-height: 0; height: 100%; font-size: 12px; }
  figcaption { font-size: 11.5px; font-weight: 600; letter-spacing: .02em; color: var(--muted); text-transform: uppercase; }
  .plot { position: relative; flex: 1 1 auto; min-height: 40px; min-width: 0; }
  svg { display: block; overflow: visible; touch-action: pan-y; }
  .grid { stroke: color-mix(in srgb, currentColor 12%, transparent); stroke-width: 1; }
  .axis { fill: var(--muted); font-size: 10.5px; font-variant-numeric: tabular-nums; }
  .limit { stroke: var(--warn); stroke-width: 1.3; stroke-dasharray: 5 4; }
  .limit.bad { stroke: var(--bad); }
  .cursor { stroke: color-mix(in srgb, currentColor 35%, transparent); stroke-width: 1; }
  .tip { position: absolute; top: 0; transform: translateX(-50%); pointer-events: none; z-index: 2; background: var(--surface-solid); border: 1px solid var(--border-strong);
    border-radius: var(--r-xs); padding: 5px 8px; box-shadow: var(--shadow); font-size: 11.5px; line-height: 1.45; white-space: nowrap; }
  .tip .tt { color: var(--muted); }
  .tip i, .legend i { display: inline-block; width: 8px; height: 8px; border-radius: 2px; margin-right: 5px; vertical-align: 0; }
  .legend { margin: 0; padding: 0; list-style: none; display: grid; gap: 3px; font-size: 11.5px; color: var(--muted); }
  .legend.row { display: flex; flex-wrap: wrap; gap: 4px 12px; }
  .legend b { color: var(--text); font-weight: 600; margin-left: 2px; }
  .note { font-size: 11.5px; color: var(--muted); }
  .hbars { display: grid; gap: 4px; }
  .hbar { display: grid; grid-template-columns: minmax(0, max-content) 1fr auto; gap: 8px; align-items: center; min-width: 0; }
  .hl { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; max-width: 12em; }
  .track { height: 8px; border-radius: 4px; background: color-mix(in srgb, currentColor 10%, transparent); overflow: hidden; }
  .track i { display: block; height: 100%; border-radius: 4px; background: var(--accent); }
  .hv { font-variant-numeric: tabular-nums; color: var(--muted); }
  .donut { display: flex; align-items: center; gap: 14px; min-width: 0; }
  .donut svg { flex: none; }
  .mid { fill: var(--text); font-size: 15px; font-weight: 600; }
  .tiny .hbars { gap: 2px; }
</style>
