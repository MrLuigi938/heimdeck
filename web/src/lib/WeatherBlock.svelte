<script>
  import { t } from './i18n.svelte.js';
  import WeatherIcon from './WeatherIcon.svelte';

  // The weather block: now, the next hours as a curve with rain bars, the
  // coming days with their range – as much as its size allows. With "mood"
  // its background follows the weather and the time of day (a few drops or
  // flakes falling; still when the system asks for less motion).
  // shape: mini | small | band | medium | large.
  let { w, opt, shape } = $props();

  const r = (v) => {
    const n = Math.round(v);
    return (n === 0 ? 0 : n) + '°';
  };
  let mood = $derived(opt.mood ? `${w.kind}-${w.day ? 'day' : 'night'}` : '');
  let still = matchMedia('(prefers-reduced-motion: reduce)').matches;
  // A few drops or flakes; fixed per block, so redrawing does not move them.
  const particles = Array.from({ length: 18 }, () => ({ x: Math.random() * 100, d: 0.6 + Math.random() * 0.6, delay: -Math.random() * 3, s: 0.6 + Math.random() * 0.8 }));
  let falling = $derived(opt.mood && !still ? (['rain', 'heavy', 'drizzle', 'storm'].includes(w.kind) ? 'rain' : w.kind === 'snow' ? 'snow' : '') : '');

  // ---- the hours ----
  let hours = $derived((w.hours || []).slice(0, Math.max(6, Math.min(25, Number(opt.hours) || 24))));
  let chartW = $state(0);
  // As many labels as fit, evenly spread.
  let step = $derived(Math.max(1, Math.ceil(hours.length / Math.max(2, Math.floor((chartW || 300) / 46)))));
  let curve = $derived.by(() => {
    if (hours.length < 2) return null;
    const temps = hours.map((h) => h.temp);
    const lo = Math.min(...temps), hi = Math.max(...temps);
    const span = Math.max(hi - lo, 4);
    const mid = (hi + lo) / 2;
    const W = 100, H = 100;
    const x = (i) => (i / (hours.length - 1)) * W;
    const y = (v) => 12 + (H - 46) * (1 - (v - (mid - span / 2)) / span);
    const pts = hours.map((h, i) => [x(i), y(h.temp)]);
    let d = `M${pts[0][0]},${pts[0][1]}`;
    for (let i = 1; i < pts.length; i++) {
      const [x0, y0] = pts[i - 1], [x1, y1] = pts[i];
      const cx = (x0 + x1) / 2;
      d += ` C${cx},${y0} ${cx},${y1} ${x1},${y1}`;
    }
    const maxMm = Math.max(1, ...hours.map((h) => h.precip));
    const bars = hours.map((h, i) => ({ x: x(i), h: h.precip > 0 ? 4 + 22 * Math.min(1, h.precip / maxMm) : h.chance >= 30 ? 2 : 0, o: 0.25 + Math.min(0.75, h.chance / 100) }));
    return { d, area: `${d} L${W},${H - 30} L0,${H - 30} Z`, pts, bars, barW: (W / hours.length) * 0.62 };
  });

  // ---- the days: the range of each day on the scale of the whole week ----
  let days = $derived(opt.days === false ? [] : w.days || []);
  let range = $derived.by(() => {
    if (!days.length) return null;
    const lo = Math.min(...days.map((d) => d.low)), hi = Math.max(...days.map((d) => d.high));
    return { lo, span: Math.max(1, hi - lo) };
  });
  // Days that fit below the hours (each row has its own height in the grid).
  let daysEl = $state();
  let daysH = $state(0);
  let dayRows = $derived(Math.max(0, Math.min(days.length, Math.floor((daysH + 4) / 26))));
</script>

<div class="wx shape-{shape}" class:mood={!!mood} data-mood={mood || null}>
  {#if falling}
    <div class="fx {falling}" aria-hidden="true">
      {#each particles as p, i (i)}<i style:left="{p.x}%" style:animation-duration="{falling === 'snow' ? p.d * 6 : p.d}s" style:animation-delay="{p.delay}s" style:--s={p.s}></i>{/each}
    </div>
  {/if}
  <div class="now">
    <WeatherIcon kind={w.kind} day={w.day} size="var(--icon)" />
    <div class="temp">{r(w.temp)}</div>
    {#if shape !== 'mini'}
      <div class="what">
        <b>{w.text}</b>
        <span>{t('gefühlt {t}', { t: r(w.feels) })}{#if shape !== 'small'} · {t('Wind {v} {u}', { v: Math.round(w.wind), u: w.speed })}{/if}</span>
      </div>
      {#if shape !== 'small' && w.high != null}
        <div class="hilo"><span>↑ {r(w.high)} ↓ {r(w.low)}</span><span class="place">{w.place}</span></div>
      {/if}
    {/if}
  </div>
  {#if shape === 'small' && (w.say || w.high != null)}
    <div class="say">{w.say || `↑ ${r(w.high)} ↓ ${r(w.low)}`}</div>
  {/if}
  {#if (shape === 'medium' || shape === 'large') && w.say}<div class="say">☂ {w.say}</div>{/if}
  {#if shape !== 'mini' && shape !== 'small' && curve}
    <div class="hours" bind:clientWidth={chartW}>
      <svg viewBox="0 0 100 100" preserveAspectRatio="none" class="chart" aria-hidden="true">
        <defs>
          <linearGradient id="wx-area" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="var(--ib-accent)" stop-opacity=".28" /><stop offset="1" stop-color="var(--ib-accent)" stop-opacity="0" /></linearGradient>
        </defs>
        {#each curve.bars as b, i (i)}
          {#if b.h}<rect x={Math.max(0, b.x - curve.barW / 2)} y={70 - b.h} width={curve.barW} height={b.h} rx="1" class="bar" opacity={b.o} />{/if}
        {/each}
        <path d={curve.area} fill="url(#wx-area)" />
        <path d={curve.d} class="line" />
      </svg>
      <div class="labels">
        {#each hours as h, i (i)}
          {#if i % step === 0}
            <div class="lab" style:left="{(i / (hours.length - 1)) * 100}%" class:first={i === 0} class:last={i === hours.length - 1}>
              <span class="lt" style:top="{curve.pts[i][1]}%">{r(h.temp)}</span>
              <span class="lb">{#if shape !== 'band'}<WeatherIcon kind={h.kind} day={h.day} animate={false} size="1.25em" />{/if}{i === 0 ? t('Jetzt') : h.time.slice(0, 2)}</span>
            </div>
          {/if}
        {/each}
      </div>
    </div>
  {/if}
  {#if (shape === 'large' || shape === 'band') && days.length}
    <div class="days" bind:this={daysEl} bind:clientHeight={daysH}>
      {#each days.slice(0, shape === 'band' ? 3 : dayRows) as d (d.name)}
        <div class="day" title={d.text}>
          <span class="dn">{d.name}</span>
          <WeatherIcon kind={d.kind} day animate={false} size="1.35em" />
          {#if shape !== 'band'}
            <span class="chance">{d.chance >= 30 ? `${Math.round(d.chance)} %` : ''}</span>
            <span class="lo">{r(d.low)}</span>
            <span class="range"><i style:left="{((d.low - range.lo) / range.span) * 100}%" style:right="{100 - ((d.high - range.lo) / range.span) * 100}%"></i></span>
            <span class="hi">{r(d.high)}</span>
          {:else}
            <!-- flat band: high and low in one line, so the day fits two rows -->
            <span class="hl">{r(d.high)} <span class="lo">{r(d.low)}</span></span>
          {/if}
        </div>
      {/each}
    </div>
  {/if}
</div>

<style>
  .wx { --icon: clamp(28px, min(22cqw, 30cqh), 110px); position: relative; height: 100%; display: flex; flex-direction: column; gap: clamp(4px, 2.2cqh, 12px);
    padding: clamp(8px, 4cqmin, 18px); box-sizing: border-box; overflow: hidden; font-size: calc(clamp(11px, min(3.4cqw, 5cqh), 16px) * var(--ib-scale, 1)); text-align: left; }
  .now { display: flex; align-items: center; gap: .6em; min-width: 0; flex: none; position: relative; }
  .temp { font-size: calc(var(--icon) * .82 * var(--ib-scale, 1)); font-weight: 300; line-height: 1; font-variant-numeric: tabular-nums; }
  .what { display: flex; flex-direction: column; min-width: 0; line-height: 1.3; }
  .what b { font-weight: 600; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .what span, .hilo { opacity: .72; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .hilo { margin-left: auto; display: flex; flex-direction: column; align-items: flex-end; flex: none; padding-left: .5em; line-height: 1.3; }
  .place { max-width: 12em; overflow: hidden; text-overflow: ellipsis; }
  .say { flex: none; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; position: relative; }

  /* mini: symbol and temperature in one line, as large as the block */
  .shape-mini { --icon: min(48cqh, 30cqw); flex-direction: row; align-items: center; justify-content: center; padding: 4px 8px; }
  .shape-mini .temp { font-size: calc(min(56cqh, 28cqw) * var(--ib-scale, 1)); font-weight: 500; }
  .shape-small { --icon: clamp(26px, min(26cqw, 40cqh), 90px); justify-content: center; }
  .shape-small .temp { font-size: calc(min(20cqw, 34cqh) * var(--ib-scale, 1)); }
  @container (max-width: 210px) { .shape-small .what { display: none; } }
  /* band: wide and flat – now, hours, three days side by side */
  .shape-band { --icon: clamp(26px, 52cqh, 90px); flex-direction: row; align-items: stretch; gap: clamp(10px, 2.4cqw, 28px); }
  .shape-band .now { flex: 0 1 auto; }
  .shape-band .hilo { display: none; }
  .shape-band .hours { flex: 1 1 40%; min-width: 120px; }
  .shape-band .days { flex: 0 0 auto; flex-direction: row; gap: 1.2em; align-items: center; }
  .shape-band .day { grid-template-columns: auto; justify-items: center; gap: 1px; height: auto; line-height: 1.15; }
  .hl { white-space: nowrap; font-size: .92em; }
  @container (max-width: 820px) { .shape-band .days { display: none; } }
  @container (max-height: 125px) { .shape-medium .hours, .shape-large .hours { display: none; } }
  @container (max-width: 520px) { .shape-band .what { display: none; } }

  .hours { position: relative; flex: 1 1 auto; min-height: 50px; }
  /* large: the hours take three parts of the room below, the days two. */
  .shape-large .hours { flex: 3 1 0; min-height: 70px; max-height: 260px; }
  .chart, .labels { position: absolute; left: 0; right: 0; top: 16px; height: calc(100% - 38px); width: 100%; overflow: visible; }
  .line { fill: none; stroke: var(--ib-accent); stroke-width: 2.4; vector-effect: non-scaling-stroke; stroke-linecap: round; }
  .bar { fill: #5ec4ff; }
  .labels { pointer-events: none; }
  .lab { position: absolute; top: 0; bottom: 0; width: 0; font-size: .86em; white-space: nowrap; --tx: -50%; }
  .lab.first { --tx: 0%; }
  .lab.last { --tx: -100%; }
  /* The temperature sits just above the curve at its height. */
  .lt { position: absolute; left: 0; transform: translate(var(--tx), calc(-100% - 3px)); font-weight: 600; }
  .lb { position: absolute; left: 0; bottom: -22px; height: 22px; transform: translateX(var(--tx)); display: flex; align-items: center; gap: 2px; opacity: .7; }

  .days { display: flex; flex-direction: column; gap: 2px; flex: 2 1 0; min-height: 0; overflow: hidden; }
  .day { display: grid; grid-template-columns: 2.4em 1.6em 3em 2.4em 1fr 2.4em; align-items: center; gap: .5em; height: 24px; flex: none; }
  .dn { font-weight: 600; }
  .chance { color: #5ec4ff; font-size: .85em; text-align: right; }
  .lo { opacity: .65; text-align: right; }
  .hi { text-align: right; }
  .range { position: relative; height: 5px; border-radius: 3px; background: color-mix(in srgb, currentColor 13%, transparent); }
  .range i { position: absolute; top: 0; bottom: 0; border-radius: 3px; background: linear-gradient(90deg, #5ec4ff, #ffd166 60%, #ff9150); min-width: 6px; }

  /* mood: the sky as background, light text on it in every theme */
  .wx.mood { color: #f5f7fb; border-radius: inherit; text-shadow: 0 1px 2px rgb(0 0 0 / .25); }
  .wx.mood::before { content: ''; position: absolute; inset: 0; background: var(--sky); z-index: 0; }
  .wx.mood > :not(.fx) { position: relative; z-index: 1; }
  [data-mood='clear-day'] { --sky: linear-gradient(165deg, #3d8bfd 0%, #62a8ff 55%, #9cc9ff 100%); }
  [data-mood='partly-day'] { --sky: linear-gradient(165deg, #4b84d6 0%, #79a6dd 60%, #a9c2df 100%); }
  [data-mood='cloudy-day'], [data-mood='fog-day'] { --sky: linear-gradient(170deg, #6f7f95 0%, #8e9aab 60%, #a9b2bf 100%); }
  [data-mood='drizzle-day'], [data-mood='rain-day'] { --sky: linear-gradient(175deg, #4d5d74 0%, #627089 55%, #7b879a 100%); }
  [data-mood='heavy-day'], [data-mood='storm-day'] { --sky: linear-gradient(175deg, #2e3747 0%, #434e62 60%, #586378 100%); }
  [data-mood='snow-day'] { --sky: linear-gradient(170deg, #8aa0bb 0%, #a9bad0 60%, #cad6e4 100%); }
  [data-mood$='-night'] { --sky: linear-gradient(170deg, #0d1426 0%, #18223b 55%, #26314f 100%); }
  [data-mood='clear-night'] { --sky: radial-gradient(1px 1px at 20% 30%, #fff8 50%, transparent), radial-gradient(1px 1px at 70% 20%, #fff8 50%, transparent), radial-gradient(1.5px 1.5px at 45% 12%, #fffa 50%, transparent), radial-gradient(1px 1px at 85% 45%, #fff6 50%, transparent), linear-gradient(170deg, #0a1022 0%, #141c35 60%, #1f2947 100%); }
  [data-mood='rain-night'], [data-mood='heavy-night'], [data-mood='storm-night'], [data-mood='drizzle-night'] { --sky: linear-gradient(175deg, #0f141e 0%, #1b2230 60%, #283041 100%); }
  .fx { position: absolute; inset: 0; overflow: hidden; pointer-events: none; z-index: 0; }
  .fx i { position: absolute; top: -12%; display: block; }
  .fx.rain i { width: 1px; height: calc(14px * var(--s)); background: linear-gradient(transparent, rgb(255 255 255 / .55)); animation: wx-rain linear infinite; }
  .fx.snow i { width: calc(4px * var(--s)); height: calc(4px * var(--s)); border-radius: 50%; background: rgb(255 255 255 / .85); animation: wx-snow linear infinite; }
  @keyframes wx-rain { to { transform: translate(-6px, 120cqh); } }
  @keyframes wx-snow { 50% { transform: translate(8px, 60cqh); } to { transform: translate(-4px, 120cqh); } }
</style>
