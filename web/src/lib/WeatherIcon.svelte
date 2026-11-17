<script>
  // Drawn weather symbols: sun and moon, clouds, rain, snow, fog and lightning.
  // Moving gently (the sun's rays turn, clouds drift, drops fall) only with
  // animate and when the system does not ask for less motion; only transform
  // and opacity move, so even old tablets keep up.
  // size goes into the style, not the width attribute: SVG attributes do not
  // take var(), and the symbol would fill the whole block.
  // kind: clear, partly, cloudy, fog, drizzle, rain, heavy, snow, storm
  let { kind = 'cloudy', day = true, animate = true, size = '1em' } = $props();

  let sky = $derived(kind === 'clear' || kind === 'partly');
  let cloud = $derived(kind !== 'clear');
  let drops = $derived(kind === 'drizzle' ? 2 : kind === 'rain' ? 3 : kind === 'heavy' || kind === 'storm' ? 4 : 0);
  // Behind a cloud the sun or moon sits higher and smaller.
  let small = $derived(kind === 'partly' || kind === 'drizzle');
</script>

<svg class="wi" class:anim={animate} viewBox="0 0 64 64" style:width={size} style:height={size} aria-hidden="true">
  {#if sky || (day && (kind === 'drizzle'))}
    {#if day}
      <g class="sun" style:transform-origin={small ? '24px 22px' : '32px 32px'}>
        {#each { length: 8 }, i (i)}
          <line x1={small ? 24 : 32} y1={small ? 6 : 6} x2={small ? 24 : 32} y2={small ? 10 : 12} stroke="#ffc94a" stroke-width="3.2" stroke-linecap="round"
            transform="rotate({i * 45} {small ? 24 : 32} {small ? 22 : 32})" />
        {/each}
      </g>
      <circle cx={small ? 24 : 32} cy={small ? 22 : 32} r={small ? 8.5 : 12} fill="#ffc94a" />
    {:else}
      <path class="moon" d={small ? 'M27 9a12 12 0 1 0 9 19A10 10 0 0 1 27 9z' : 'M30 12a19 19 0 1 0 16 28A16 16 0 0 1 30 12z'} fill="#dfe6f5" />
    {/if}
  {/if}
  {#if cloud}
    {#if kind === 'cloudy' || kind === 'storm' || kind === 'heavy'}
      <path class="cloud back" d="M30 30h20a8 8 0 0 0 0-16 11 11 0 0 0-20-2 8 8 0 0 0 0 18z" fill="#9aa7bb" opacity=".8" />
    {/if}
    <path class="cloud" d={kind === 'fog' ? 'M14 30h32a9 9 0 0 0 0-18 13 13 0 0 0-24-3A10 10 0 0 0 14 30z' : 'M16 44h30a10 10 0 0 0 0-20 14 14 0 0 0-27-3A11.5 11.5 0 0 0 16 44z'}
      fill={kind === 'storm' || kind === 'heavy' ? '#b4bfd0' : '#e6ebf3'} />
  {/if}
  {#if kind === 'fog'}
    <g class="fog">
      <line x1="10" y1="38" x2="50" y2="38" stroke="#c9d2e0" stroke-width="3.2" stroke-linecap="round" />
      <line x1="16" y1="46" x2="56" y2="46" stroke="#c9d2e0" stroke-width="3.2" stroke-linecap="round" />
      <line x1="10" y1="54" x2="44" y2="54" stroke="#c9d2e0" stroke-width="3.2" stroke-linecap="round" />
    </g>
  {/if}
  {#each { length: drops }, i (i)}
    <line class="drop" style:animation-delay="{i * 0.27}s" x1={20 + i * 8} y1="49" x2={18 + i * 8} y2="56" stroke="#5ec4ff" stroke-width="3" stroke-linecap="round" />
  {/each}
  {#if kind === 'snow'}
    {#each [18, 30, 42] as x, i (x)}
      <circle class="flake" style:animation-delay="{i * 0.6}s" cx={x} cy="52" r="2.6" fill="#f2f6ff" />
    {/each}
  {/if}
  {#if kind === 'storm'}
    <path class="bolt" d="M34 42l-7 11h6l-3 9 10-13h-6l4-7z" fill="#ffd34a" />
  {/if}
</svg>

<style>
  .wi { display: block; flex: none; overflow: visible; }
  /* A soft outline keeps the light clouds visible on light cards. */
  .cloud { stroke: color-mix(in srgb, currentColor 22%, transparent); stroke-width: 1.2; }
  .anim .sun { animation: wi-turn 36s linear infinite; }
  .anim .cloud { animation: wi-drift 7s ease-in-out infinite alternate; }
  .anim .cloud.back { animation-duration: 9s; animation-direction: alternate-reverse; }
  .anim .drop { animation: wi-fall 1.1s linear infinite; }
  .anim .flake { animation: wi-snow 2.4s ease-in-out infinite; }
  .anim .fog { animation: wi-drift 5s ease-in-out infinite alternate; }
  .anim .bolt { animation: wi-flash 4s steps(1) infinite; }
  @keyframes wi-turn { to { transform: rotate(360deg); } }
  @keyframes wi-drift { from { transform: translateX(-1.5px); } to { transform: translateX(2.5px); } }
  @keyframes wi-fall { from { transform: translateY(-5px); opacity: 0; } 30% { opacity: 1; } to { transform: translateY(6px); opacity: 0; } }
  @keyframes wi-snow { 0% { transform: translate(0, -6px); opacity: 0; } 30% { opacity: 1; } 100% { transform: translate(2px, 6px); opacity: 0; } }
  @keyframes wi-flash { 0%, 86%, 92% { opacity: 1; } 89% { opacity: .15; } }
  @media (prefers-reduced-motion: reduce) {
    .anim * { animation: none !important; }
  }
</style>
