<script>
  import { untrack } from 'svelte';
  import { adventCandles } from './themes.js';

  // Moving decoration of the seasonal themes ("Saison-Deko"): bats at
  // Halloween, fireworks at New Year, snow and a string of lights at Christmas,
  // an Advent wreath, a hopping bunny at Easter, soap bubbles at the start of
  // summer. It runs behind the tiles, never takes a click (pointer-events:
  // none) and stays sparse.
  // With "reduce motion" in the system only the still parts are shown.
  let { season } = $props();

  const still = matchMedia('(prefers-reduced-motion: reduce)').matches;
  const rnd = (a, b) => a + Math.random() * (b - a);
  const many = (n, make) => Array.from({ length: n }, (_, i) => make(i));

  // Falling snow and rising bubbles: fixed sets, each with its own speed and start.
  const flakes = many(34, () => ({ x: rnd(0, 100), size: rnd(3, 7), dur: rnd(9, 18), delay: -rnd(0, 18), drift: rnd(-30, 30), o: rnd(0.4, 0.9) }));
  const bubbles = many(14, () => ({ x: rnd(2, 98), size: rnd(14, 38), dur: rnd(12, 22), delay: -rnd(0, 22), drift: rnd(-40, 40) }));
  const bulbs = many(26, (i) => ({ x: (i + 0.5) * (100 / 26), color: ['#ff5d5d', '#ffd166', '#5ed3ff', '#7ee081'][i % 4], delay: rnd(0, 2) }));

  // Bats and fireworks come and go: a timer adds a flock or a burst and removes it again.
  let flocks = $state([]);
  let bursts = $state([]);
  let next = 0;
  $effect(() => untrack(() => start(season)));
  // Runs untracked: adding to flocks/bursts must not restart the effect.
  function start(season) {
    if (still) return;
    const timers = [];
    const later = (ms, fn) => timers.push(setTimeout(fn, ms));
    if (season === 'halloween') {
      const flock = () => {
        const id = next++;
        const dur = rnd(7, 10);
        flocks.push({ id, top: rnd(8, 60), dur, rtl: Math.random() < 0.5, bats: many(Math.round(rnd(3, 5)), () => ({ dy: rnd(-40, 40), delay: rnd(0, 1.2), size: rnd(18, 30) })) });
        later((dur + 1.5) * 1000, () => (flocks = flocks.filter((f) => f.id !== id)));
      };
      later(1500, flock);
      timers.push(setInterval(flock, 15000));
    }
    if (season === 'newyear') {
      // A rocket climbs, then bursts into two rings of streaks that fall with
      // gravity and fade; now and then a slow golden "willow" instead.
      const colors = ['#f5c84c', '#ff6b7a', '#7cc8ff', '#b693ff', '#7ee0a1', '#ffffff'];
      const pick = () => colors[Math.floor(rnd(0, colors.length))];
      const burst = () => {
        const id = next++;
        const willow = Math.random() < 0.25;
        const [c1, c2] = willow ? ['#f5c84c', '#ffe7a3'] : [pick(), pick()];
        const y = rnd(5, 24); // high up, where the sky over the header is free
        const rise = rnd(0.8, 1.1);
        const ring = (n, r0, r1, color, len) =>
          many(n, (i) => ({ deg: (i / n) * 360 + rnd(-4, 4), r: rnd(r0, r1), color, len: rnd(len * 0.7, len) }));
        const sparks = willow ? ring(34, 100, 160, c1, 34) : [...ring(28, 120, 180, c1, 24), ...ring(14, 55, 90, c2, 16)];
        bursts.push({ id, x: rnd(12, 88), y, rise, willow, sparks, dur: willow ? 2.8 : 1.6, c: c1 });
        later((rise + (willow ? 3 : 1.9)) * 1000, () => (bursts = bursts.filter((b) => b.id !== id)));
      };
      later(300, burst);
      timers.push(setInterval(() => {
        burst();
        if (Math.random() < 0.3) later(rnd(250, 600), burst); // sometimes two at once
      }, 2000));
    }
    return () => timers.forEach((t) => (clearTimeout(t), clearInterval(t)));
  }

  const candles = adventCandles();
  // New Year's starry sky, upper part of the screen; it twinkles unless motion is reduced.
  const stars = many(80, () => ({ x: rnd(0, 100), y: rnd(0, 75), size: rnd(1, 2.6), delay: -rnd(0, 4), dur: rnd(2.5, 5) }));
</script>

<div class="fx" class:still aria-hidden="true">
  {#if season === 'christmas' || season === 'advent'}
    {#if !still}
      {#each season === 'advent' ? flakes.slice(0, 14) : flakes as f}
        <span class="flake" style:left="{f.x}%" style:width="{f.size}px" style:height="{f.size}px" style:opacity={f.o}
          style:animation-duration="{f.dur}s" style:animation-delay="{f.delay}s" style:--drift="{f.drift}px"></span>
      {/each}
    {/if}
    {#if season === 'christmas'}
      <div class="garland">
        <svg class="wire" viewBox="0 0 100 10" preserveAspectRatio="none"><path d="M0 2 Q 6.25 9 12.5 2 T 25 2 T 37.5 2 T 50 2 T 62.5 2 T 75 2 T 87.5 2 T 100 2" /></svg>
        {#each bulbs as b, i}
          <span class="bulb" style:left="{b.x}%" style:top="{4 + (i % 2 ? 0 : 7)}px" style:background={b.color} style:--glow={b.color} style:animation-delay="{b.delay}s"></span>
        {/each}
      </div>
    {:else}
      <!-- Advent wreath: one candle more each Advent Sunday. -->
      <div class="wreath" title="{candles}. Advent">
        {#each [1, 2, 3, 4] as c}
          <span class="candle" class:lit={c <= candles}><span class="flame"></span></span>
        {/each}
        <span class="green"></span>
      </div>
    {/if}
  {/if}

  {#if season === 'halloween'}
    {#each flocks as fl (fl.id)}
      <div class="flock" class:rtl={fl.rtl} style:top="{fl.top}%" style:animation-duration="{fl.dur}s">
        {#each fl.bats as b}
          <svg class="bat" style:width="{b.size}px" style:transform="translateY({b.dy}px)" style:animation-delay="{b.delay}s" viewBox="0 0 64 32">
            <path d="M32 10c2-4 5-5 5-5l1 5c4-3 9-3 13-1 3 1 6 4 13 2-4 4-7 7-13 7-2 0-4 2-5 5-3-3-7-3-9 0-2-1-3-3-5-3-2 0-3 2-5 3-2-3-6-3-9 0-1-3-3-5-5-5-6 0-9-3-13-7 7 2 10-1 13-2 4-2 9-2 13 1l1-5s3 1 5 5z" />
          </svg>
        {/each}
      </div>
    {/each}
  {/if}

  {#if season === 'newyear'}
    {#each stars as st}
      <span class="star" style:left="{st.x}%" style:top="{st.y}%" style:width="{st.size}px" style:height="{st.size}px"
        style:animation-duration="{st.dur}s" style:animation-delay="{st.delay}s"></span>
    {/each}
    {#each bursts as b (b.id)}
      <div class="burst" class:willow={b.willow} style:left="{b.x}%" style:top="{b.y}%" style:--rise="{b.rise}s" style:--rise-from="{100 - b.y}vh" style:--dur="{b.dur}s" style:--c={b.c}>
        <span class="rocket"></span>
        <span class="flash"></span>
        {#each b.sparks as s}
          <span class="spark" style:--deg="{s.deg}deg" style:--r="{s.r}px" style:--len="{s.len}px" style:--c={s.color}></span>
        {/each}
      </div>
    {/each}
  {/if}

  {#if season === 'easter' && !still}
    <span class="bunny">🐇</span>
  {/if}

  {#if season === 'summer' && !still}
    {#each bubbles as b}
      <span class="bubble" style:left="{b.x}%" style:width="{b.size}px" style:height="{b.size}px"
        style:animation-duration="{b.dur}s" style:animation-delay="{b.delay}s" style:--drift="{b.drift}px"></span>
    {/each}
  {/if}
</div>

<style>
  /* Behind the dashboard (which has z-index 1), in front of the background. */
  .fx { position: fixed; inset: 0; z-index: 0; pointer-events: none; overflow: hidden; }

  /* Snow */
  .flake { position: absolute; top: -10px; border-radius: 50%; background: #fff; box-shadow: 0 0 6px rgba(255, 255, 255, .6); animation: fall linear infinite; }
  @keyframes fall { to { transform: translate(var(--drift), 105vh); } }

  /* String of lights along the top edge */
  .garland { position: absolute; left: 0; right: 0; top: 0; height: 22px; }
  .wire { position: absolute; inset: 0 0 auto 0; width: 100%; height: 14px; }
  .wire path { fill: none; stroke: rgba(40, 60, 40, .9); stroke-width: .6; vector-effect: non-scaling-stroke; }
  .bulb { position: absolute; width: 8px; height: 11px; border-radius: 50% 50% 45% 45%; transform: translateX(-50%); box-shadow: 0 0 8px var(--glow); animation: twinkle 1.6s ease-in-out infinite alternate; }
  @keyframes twinkle { from { opacity: .45; box-shadow: 0 0 2px var(--glow); } to { opacity: 1; box-shadow: 0 0 12px var(--glow); } }

  /* Advent wreath in the lower left corner */
  .wreath { position: absolute; left: 18px; bottom: 16px; display: flex; gap: 10px; align-items: flex-end; padding: 0 10px 8px; }
  .green { position: absolute; left: 0; right: 0; bottom: 0; height: 14px; border-radius: 999px; background: radial-gradient(circle at 20% 50%, #c0392b 0 3px, transparent 4px), radial-gradient(circle at 70% 40%, #c0392b 0 3px, transparent 4px), linear-gradient(#2f6b3a, #1d4a27); box-shadow: 0 4px 12px rgba(0, 0, 0, .35); }
  .candle { position: relative; width: 9px; height: 26px; border-radius: 2px; background: linear-gradient(#f3e6c8, #d8c39a); }
  .candle:nth-child(2) { height: 30px; } .candle:nth-child(3) { height: 24px; } .candle:nth-child(4) { height: 28px; }
  .flame { position: absolute; left: 50%; bottom: 100%; width: 7px; height: 11px; margin-left: -3.5px; border-radius: 50% 50% 50% 50% / 60% 60% 40% 40%; background: radial-gradient(circle at 50% 70%, #fff6c2, #ffb52e 55%, #ff7b1a); opacity: 0; }
  .candle.lit .flame { opacity: 1; box-shadow: 0 0 10px 3px rgba(255, 170, 60, .55); animation: flicker 1.3s ease-in-out infinite alternate; }
  @keyframes flicker { from { transform: scaleY(1) rotate(-2deg); } to { transform: scaleY(1.15) rotate(2deg); } }

  /* Bats crossing the screen */
  .flock { position: absolute; left: 0; display: flex; gap: 14px; animation: fly linear forwards; }
  .flock.rtl { animation-name: fly-back; }
  @keyframes fly { from { transform: translateX(-160px); } to { transform: translateX(calc(100vw + 40px)); } }
  @keyframes fly-back { from { transform: translateX(calc(100vw + 40px)) scaleX(-1); } to { transform: translateX(-160px) scaleX(-1); } }
  .bat { fill: #120a18; filter: drop-shadow(0 0 2px rgba(255, 140, 40, .35)); animation: flap .28s ease-in-out infinite alternate; }
  @keyframes flap { to { transform: scaleY(.45); } }

  /* Starry sky for New Year's Eve */
  .star { position: absolute; border-radius: 50%; background: #fff; box-shadow: 0 0 4px rgba(255, 255, 255, .8); animation: twinkle-star ease-in-out infinite alternate; }
  @keyframes twinkle-star { from { opacity: .25; } to { opacity: .95; } }
  .still .star { animation: none; opacity: .7; }

  /* Fireworks: rocket climbs, flash, two rings of falling streaks */
  .burst { position: absolute; width: 0; height: 0; }
  .rocket { position: absolute; left: -1.5px; width: 3px; height: 14px; border-radius: 2px; background: linear-gradient(transparent, #fff6d0);
    box-shadow: 0 0 6px #ffe7a3; animation: rocket var(--rise) cubic-bezier(.25, .7, .4, 1) forwards; }
  @keyframes rocket { from { transform: translateY(var(--rise-from)); opacity: 1; } 92% { opacity: 1; } to { transform: translateY(0); opacity: 0; } }
  .flash { position: absolute; left: -70px; top: -70px; width: 140px; height: 140px; border-radius: 50%;
    background: radial-gradient(circle, color-mix(in srgb, var(--c) 70%, #fff) 0, transparent 65%); opacity: 0; animation: flash .5s ease-out var(--rise) both; }
  @keyframes flash { 0% { opacity: .9; transform: scale(.4); } 100% { opacity: 0; transform: scale(1.4); } }
  /* A streak: bright at its outer tip, fading back towards the centre. */
  .spark { position: absolute; left: -1.5px; top: 0; width: 3px; height: var(--len); border-radius: 3px; transform-origin: 50% 0;
    background: linear-gradient(transparent, var(--c) 70%, #fff); box-shadow: 0 0 8px var(--c), 0 0 2px #fff; opacity: 0;
    animation: spark var(--dur) cubic-bezier(.12, .7, .35, 1) var(--rise) both; }
  @keyframes spark {
    0% { transform: rotate(var(--deg)) translateY(0) scaleY(.2); opacity: 1; }
    55% { opacity: 1; }
    100% { transform: translateY(46px) rotate(var(--deg)) translateY(var(--r)) scaleY(1); opacity: 0; }
  }
  /* Willow: golden, slower, hanging down further */
  .willow .spark { animation-timing-function: cubic-bezier(.1, .6, .3, 1); }
  .willow .spark { animation-name: spark-willow; }
  @keyframes spark-willow {
    0% { transform: rotate(var(--deg)) translateY(0) scaleY(.2); opacity: 1; }
    60% { opacity: .95; }
    100% { transform: translateY(120px) rotate(var(--deg)) translateY(var(--r)) scaleY(1.4); opacity: 0; }
  }

  /* Easter: a bunny hops along the bottom, eggs in the corner */
  /* The bunny emoji looks to the left; mirrored, it looks where it hops. */
  @keyframes hop-across { 0% { transform: translateX(0) scaleX(-1); } 45%, 100% { transform: translateX(calc(100vw + 120px)) scaleX(-1); } }
  .bunny { position: absolute; bottom: 10px; left: -60px; font-size: 34px;
    animation-name: hop-across, hop; animation-duration: 22s, .55s; animation-timing-function: linear, ease-out; animation-iteration-count: infinite, infinite; animation-direction: normal, alternate; }
  @keyframes hop { to { margin-bottom: 18px; } }

  /* Summer: soap bubbles rising */
  .bubble { position: absolute; bottom: -50px; border-radius: 50%; border: 1px solid rgba(255, 255, 255, .7);
    background: radial-gradient(circle at 30% 30%, rgba(255, 255, 255, .7), rgba(180, 230, 255, .15) 45%, rgba(255, 190, 240, .18) 75%, transparent);
    animation: rise linear infinite; }
  @keyframes rise { to { transform: translate(var(--drift), -110vh); } }

  @media (max-width: 640px) { .wreath { transform: scale(.8); transform-origin: left bottom; } }
</style>
