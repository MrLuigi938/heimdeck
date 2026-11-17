<script>
  import { t, locale, i18n } from './i18n.svelte.js';

  // The large clock: digital (thin, normal, mono or segment digits), analog or
  // as a word clock ("ES IST VIERTEL NACH DREI"), with the date, a second time
  // zone and the greeting. It runs on the time of this device.
  // shape: mini | small | band | medium | large (from the block's size).
  let { opt, shape, greeting = '' } = $props();

  let now = $state(new Date());
  $effect(() => {
    // Every second with seconds or the analog face, otherwise when the minute turns.
    let timer;
    // d, not now: reading now here would make the effect depend on it, so every
    // tick would start the effect again (an endless loop that blocks the page).
    const tick = () => {
      const d = new Date();
      now = d;
      const wait = opt.seconds || opt.face === 'analog' ? 1000 - d.getMilliseconds() : 60000 - d.getSeconds() * 1000 - d.getMilliseconds();
      timer = setTimeout(tick, wait + 15);
    };
    tick();
    return () => clearTimeout(timer);
  });

  const pad = (n) => String(n).padStart(2, '0');
  let face = $derived(shape === 'mini' && opt.face !== 'digital' ? 'digital' : opt.face);
  let hm = $derived(now.toLocaleTimeString(locale(), { hour: '2-digit', minute: '2-digit' }));
  let date = $derived(
    now.toLocaleDateString(locale(), shape === 'small' || face === 'analog' ? { weekday: 'short', day: 'numeric', month: 'short' } : { weekday: 'long', day: 'numeric', month: 'long' }),
  );
  let week = $derived.by(() => {
    const d = new Date(Date.UTC(now.getFullYear(), now.getMonth(), now.getDate()));
    d.setUTCDate(d.getUTCDate() + 4 - (d.getUTCDay() || 7));
    return Math.ceil(((d - Date.UTC(d.getUTCFullYear(), 0, 1)) / 86400e3 + 1) / 7);
  });
  // The second time zone: its time and how far it is ahead or behind.
  let zone = $derived.by(() => {
    if (!opt.tz) return null;
    try {
      const time = now.toLocaleTimeString(locale(), { hour: '2-digit', minute: '2-digit', timeZone: opt.tz });
      const there = new Date(now.toLocaleString('en-US', { timeZone: opt.tz }));
      const here = new Date(now.toLocaleString('en-US'));
      const diff = Math.round((there - here) / 36e5 * 2) / 2;
      const name = opt.tzName || opt.tz.split('/').pop().replace(/_/g, ' ');
      return { name, time, diff: diff === 0 ? '' : (diff > 0 ? '+' : '−') + String(Math.abs(diff)).replace('.', ',') + ' h' };
    } catch {
      return null;
    }
  });

  // Lines under and above the digits: the digits take the height that is left.
  let lines = $derived(shape === 'mini' || shape === 'band' ? 0 : (opt.greet && greeting ? 1 : 0) + (opt.date ? 1 : 0) + (zone && shape !== 'small' ? 1 : 0));

  // ---- fitting ----
  // The sizes above follow the block (cqw/cqh), but how wide a date or a time
  // zone is depends on the language and the font. So the digital face is
  // measured, and if anything would stick out it all shrinks together (--fit).
  let box = $state();
  let inner = $state();
  let zoneText = $derived(zone ? `${zone.name} ${zone.diff}` : '');
  $effect(() => {
    if (!box || !inner) return;
    // What changes the width or height; not the time itself (its digits all have the same width).
    shape; lines; date; zoneText; greeting; opt.font; opt.seconds; opt.greet; opt.date;
    const fit = () => {
      inner.style.setProperty('--fit', '1');
      const cs = getComputedStyle(box);
      const aw = box.clientWidth - parseFloat(cs.paddingLeft) - parseFloat(cs.paddingRight);
      const ah = box.clientHeight - parseFloat(cs.paddingTop) - parseFloat(cs.paddingBottom);
      const f = Math.min(1, aw / Math.max(1, inner.scrollWidth), ah / Math.max(1, inner.scrollHeight));
      if (f < 1) inner.style.setProperty('--fit', String(Math.max(0.15, f * 0.97)));
    };
    fit();
    const ro = new ResizeObserver(fit);
    ro.observe(box);
    document.fonts?.ready.then(fit);
    return () => ro.disconnect();
  });

  // ---- analog ----
  let hourAngle = $derived((now.getHours() % 12) * 30 + now.getMinutes() * 0.5);
  let minuteAngle = $derived(now.getMinutes() * 6 + now.getSeconds() * 0.1);
  let secondAngle = $derived(now.getSeconds() * 6);

  // ---- word clock ----
  // 11 × 10 letters; words as [row, first column, length].
  const WORDS = {
    de: {
      grid: ['ESKISTAFÜNF', 'ZEHNZWANZIG', 'DREIVIERTEL', 'VORFUNKNACH', 'HALBAELFÜNF', 'EINSXAMZWEI', 'DREIPMJVIER', 'SECHSNLACHT', 'SIEBENZWÖLF', 'ZEHNEUNKUHR'],
      lead: [[0, 0, 2], [0, 3, 3]],
      m: { five: [0, 7, 4], ten: [1, 0, 4], quarter: [2, 4, 7], twenty: [1, 4, 7], half: [4, 0, 4], past: [3, 7, 4], to: [3, 0, 3], full: [9, 8, 3] },
      h: [null, [5, 0, 4], [5, 7, 4], [6, 0, 4], [6, 7, 4], [4, 7, 4], [7, 0, 5], [8, 0, 6], [7, 7, 4], [9, 3, 4], [9, 0, 4], [4, 5, 3], [8, 6, 5]],
      one: [5, 0, 3], // "EIN UHR"
      // German counts towards the next hour from :25 ("fünf vor halb vier").
      words: (m, s) => [[], [s.five, s.past], [s.ten, s.past], [s.quarter, s.past], [s.twenty, s.past], [s.five, s.to, s.half], [s.half], [s.five, s.past, s.half], [s.twenty, s.to], [s.quarter, s.to], [s.ten, s.to], [s.five, s.to]][m],
      next: 5,
    },
    en: {
      grid: ['ITLISASAMPM', 'ACQUARTERDC', 'TWENTYFIVEX', 'HALFSTENFTO', 'PASTERUNINE', 'ONESIXTHREE', 'FOURFIVETWO', 'EIGHTELEVEN', 'SEVENTWELVE', 'TENSEOCLOCK'],
      lead: [[0, 0, 2], [0, 3, 2]],
      m: { five: [2, 6, 4], ten: [3, 5, 3], a: [1, 0, 1], quarter: [1, 2, 7], twenty: [2, 0, 6], half: [3, 0, 4], past: [4, 0, 4], to: [3, 9, 2], full: [9, 5, 6] },
      h: [null, [5, 0, 3], [6, 8, 3], [5, 6, 5], [6, 0, 4], [6, 4, 4], [5, 3, 3], [8, 0, 5], [7, 0, 5], [4, 7, 4], [9, 0, 3], [7, 5, 6], [8, 5, 6]],
      one: null,
      words: (m, s) => [[], [s.five, s.past], [s.ten, s.past], [s.a, s.quarter, s.past], [s.twenty, s.past], [s.twenty, s.five, s.past], [s.half, s.past], [s.twenty, s.five, s.to], [s.twenty, s.to], [s.a, s.quarter, s.to], [s.ten, s.to], [s.five, s.to]][m],
      next: 7,
    },
  };
  let word = $derived.by(() => {
    const w = WORDS[i18n.lang === 'en' ? 'en' : 'de'];
    const step = Math.floor(now.getMinutes() / 5);
    const hour = ((now.getHours() + (step >= w.next ? 1 : 0)) % 12) || 12;
    const parts = [...w.lead, ...w.words(step, w.m)];
    parts.push(step === 0 && hour === 1 && w.one ? w.one : w.h[hour]);
    if (step === 0) parts.push(w.m.full);
    const on = new Set();
    for (const [r, c, l] of parts) for (let i = c; i < c + l; i++) on.add(r * 11 + i);
    return { letters: w.grid.join('').split(''), on, dots: now.getMinutes() % 5 };
  });
</script>

<div class="clock shape-{shape} face-{face} font-{opt.font}" style:--lines={lines} bind:this={box}>
  {#if face === 'analog'}
    <svg class="dial" viewBox="0 0 100 100" role="img" aria-label={hm}>
      <circle cx="50" cy="50" r="48" class="rim" />
      {#each { length: 60 }, i (i)}
        <line x1="50" y1={i % 5 ? 4.5 : 3.5} x2="50" y2={i % 5 ? 7.5 : 11} class:hour={i % 5 === 0} class="tick" transform="rotate({i * 6} 50 50)" />
      {/each}
      <line x1="50" y1="54" x2="50" y2="25" class="hand h" transform="rotate({hourAngle} 50 50)" />
      <line x1="50" y1="56" x2="50" y2="11" class="hand m" transform="rotate({minuteAngle} 50 50)" />
      {#if opt.seconds}
        <g transform="rotate({secondAngle} 50 50)"><line x1="50" y1="62" x2="50" y2="9" class="hand s" /><circle cx="50" cy="9" r="2.4" class="dot" /></g>
      {/if}
      <circle cx="50" cy="50" r="2.6" class="dot" />
    </svg>
    {#if opt.date && shape !== 'small'}<div class="sub">{date}</div>{/if}
  {:else if face === 'word'}
    <div class="words" role="img" aria-label={hm}>
      <span class="corner tl" class:lit={word.dots >= 1}></span><span class="corner tr" class:lit={word.dots >= 2}></span>
      <span class="corner br" class:lit={word.dots >= 3}></span><span class="corner bl" class:lit={word.dots >= 4}></span>
      <div class="letters">
        {#each word.letters as l, i (i)}<span class:lit={word.on.has(i)}>{l}</span>{/each}
      </div>
    </div>
  {:else}
    <div class="main" bind:this={inner}>
      {#if opt.greet && greeting && shape !== 'mini' && shape !== 'band'}<div class="greet">{greeting}</div>{/if}
      <div class="time">{hm}{#if opt.seconds && shape !== 'mini'}<span class="sec">{pad(now.getSeconds())}</span>{/if}</div>
      {#if face === 'text' || shape !== 'mini'}
        {#if opt.date}<div class="sub">{date}{#if shape === 'large' || shape === 'band'}<span class="kw"> · {t('KW {n}', { n: week })}</span>{/if}</div>{/if}
        {#if zone}<div class="zone">{zone.name} {zone.time}{#if zone.diff}<span class="diff"> · {zone.diff}</span>{/if}</div>{/if}
      {/if}
    </div>
  {/if}
</div>

<style>
  .clock { height: 100%; width: 100%; display: flex; flex-direction: column; align-items: var(--ib-items, center); justify-content: center; text-align: var(--ib-align, center); padding: 4cqmin 6cqmin; box-sizing: border-box; overflow: hidden; }
  .main { display: flex; flex-direction: column; align-items: inherit; max-width: 100%; }
  /* The digits fill the block: as large as its width and height allow. */
  /* Each line beside the digits takes about 15 % of the height (their font is at most 11cqh). */
  .time { font-size: calc(min(30cqw, (84cqh - var(--lines, 0) * 15cqh) / .95) * var(--ib-scale, 1) * var(--fit, 1)); line-height: .95; font-variant-numeric: tabular-nums; letter-spacing: -.02em; white-space: nowrap; color: var(--ib-color, inherit); }
  .shape-mini .time { font-size: calc(min(28cqw, 70cqh) * var(--ib-scale, 1) * var(--fit, 1)); font-weight: 600; }
  .shape-band .time { font-size: calc(min(14cqw, 58cqh) * var(--ib-scale, 1) * var(--fit, 1)); }
  .shape-band .main { flex-direction: row; align-items: baseline; gap: .6em; flex-wrap: wrap; justify-content: var(--ib-items, center); }
  .shape-band .sub, .shape-band .zone { font-size: calc(min(4cqw, 22cqh) * var(--ib-scale, 1) * var(--fit, 1)); }
  .font-thin .time { font-weight: 200; }
  .font-normal .time { font-weight: 600; }
  .font-mono .time { font-family: ui-monospace, 'Cascadia Mono', Consolas, monospace; font-weight: 500; letter-spacing: -.04em; }
  .font-segment .time { font-family: ui-monospace, Consolas, monospace; font-weight: 400; color: var(--ib-accent); text-shadow: 0 0 .25em color-mix(in srgb, var(--ib-accent) 55%, transparent); }
  .sec { font-size: .3em; font-weight: 400; opacity: .6; vertical-align: top; margin-left: .12em; position: relative; top: .2em; }
  .sub, .zone, .greet { font-size: calc(clamp(10px, min(5.5cqw, 11cqh), 30px) * var(--ib-scale, 1) * var(--fit, 1)); opacity: .75; margin-top: .3em; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; max-width: 100%; }
  .greet { margin: 0 0 .25em; opacity: .85; }
  .zone { font-size: calc(clamp(9px, min(4.2cqw, 8cqh), 22px) * var(--ib-scale, 1) * var(--fit, 1)); opacity: .6; }
  .shape-small .zone, .shape-small .greet { display: none; }
  @container (max-height: 110px) { .zone, .greet { display: none; } }
  @container (max-height: 70px) { .sub { display: none; } }

  /* The dial takes the height the date leaves; the SVG keeps its round shape inside. */
  .dial { flex: 1 1 0; min-height: 0; width: 100%; max-width: 100%; }
  .rim { fill: none; stroke: currentColor; stroke-width: 1.2; opacity: .25; }
  .tick { stroke: currentColor; stroke-width: .7; opacity: .45; }
  .tick.hour { stroke-width: 2; opacity: .9; }
  .hand { stroke: currentColor; stroke-linecap: round; }
  .hand.h { stroke-width: 4; }
  .hand.m { stroke-width: 2.6; }
  .hand.s { stroke: var(--ib-accent); stroke-width: 1.1; }
  .dot { fill: var(--ib-accent); }
  .face-analog .sub { margin-top: .2em; flex: none; }

  .words { position: relative; width: min(96cqw, 104cqh); aspect-ratio: 11 / 10.4; max-height: 100%; display: grid; place-items: center; }
  .letters { display: grid; grid-template-columns: repeat(11, 1fr); width: 88%; height: 88%; align-items: center; justify-items: center;
    font-weight: 700; font-size: calc(min(6cqw, 6.5cqh) * var(--ib-scale, 1)); line-height: 1; }
  .letters span { opacity: .2; transition: opacity .6s, color .6s; }
  .letters span.lit { opacity: 1; color: var(--ib-accent); text-shadow: 0 0 .5em color-mix(in srgb, var(--ib-accent) 45%, transparent); }
  .corner { position: absolute; width: 1.6%; aspect-ratio: 1; min-width: 3px; border-radius: 50%; background: currentColor; opacity: .2; }
  .corner.lit { opacity: 1; background: var(--ib-accent); }
  .tl { left: 1%; top: 1%; } .tr { right: 1%; top: 1%; } .br { right: 1%; bottom: 1%; } .bl { left: 1%; bottom: 1%; }
</style>
