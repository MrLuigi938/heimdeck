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
      const colors = ['#f5c84c', '#ff6b7a', '#7cc8ff', '#b693ff', '#7ee0a1'];
      const burst = () => {
        const id = next++;
        const color = colors[Math.floor(rnd(0, colors.length))];
        bursts.push({ id, x: rnd(10, 90), y: rnd(8, 40), color, sparks: many(16, (i) => ({ a: (i / 16) * Math.PI * 2, r: rnd(60, 110) })) });
        later(1600, () => (bursts = bursts.filter((b) => b.id !== id)));
      };
      burst();
      timers.push(setInterval(burst, 2400));
    }
    return () => timers.forEach((t) => (clearTimeout(t), clearInterval(t)));
  }

  const candles = adventCandles();
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
    {#each bursts as b (b.id)}
      <div class="burst" style:left="{b.x}%" style:top="{b.y}%" style:--c={b.color}>
        {#each b.sparks as s}
          <span class="spark" style:--dx="{Math.cos(s.a) * s.r}px" style:--dy="{Math.sin(s.a) * s.r}px"></span>
        {/each}
      </div>
    {/each}
  {/if}

  {#if season === 'easter'}
    {#if !still}<span class="bunny">🐇</span>{/if}
    <div class="eggs"><span>🥚</span><span style="background:#ffd6e8"></span><span style="background:#d6f0ff"></span><span style="background:#fff1b8"></span></div>
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

  /* Fireworks */
  .burst { position: absolute; width: 0; height: 0; }
  .spark { position: absolute; width: 5px; height: 5px; border-radius: 50%; background: var(--c); box-shadow: 0 0 8px var(--c); animation: spark 1.5s ease-out forwards; }
  @keyframes spark { 0% { transform: translate(0, 0); opacity: 1; } 80% { opacity: .9; } 100% { transform: translate(var(--dx), calc(var(--dy) + 40px)); opacity: 0; } }

  /* Easter: a bunny hops along the bottom, eggs in the corner */
  /* The bunny emoji looks to the left; mirrored, it looks where it hops. */
  @keyframes hop-across { 0% { transform: translateX(0) scaleX(-1); } 45%, 100% { transform: translateX(calc(100vw + 120px)) scaleX(-1); } }
  .bunny { position: absolute; bottom: 10px; left: -60px; font-size: 34px;
    animation-name: hop-across, hop; animation-duration: 22s, .55s; animation-timing-function: linear, ease-out; animation-iteration-count: infinite, infinite; animation-direction: normal, alternate; }
  @keyframes hop { to { margin-bottom: 18px; } }
  .eggs { position: absolute; right: 18px; bottom: 12px; display: flex; gap: 6px; align-items: flex-end; font-size: 24px; }
  .eggs span:not(:first-child) { width: 18px; height: 24px; border-radius: 50% 50% 45% 45% / 60% 60% 40% 40%; border: 2px solid rgba(0, 0, 0, .08); }

  /* Summer: soap bubbles rising */
  .bubble { position: absolute; bottom: -50px; border-radius: 50%; border: 1px solid rgba(255, 255, 255, .7);
    background: radial-gradient(circle at 30% 30%, rgba(255, 255, 255, .7), rgba(180, 230, 255, .15) 45%, rgba(255, 190, 240, .18) 75%, transparent);
    animation: rise linear infinite; }
  @keyframes rise { to { transform: translate(var(--drift), -110vh); } }

  @media (max-width: 640px) { .wreath { transform: scale(.8); transform-origin: left bottom; } }
</style>
