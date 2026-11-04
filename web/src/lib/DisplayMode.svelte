<script>
  import { onDestroy, onMount } from 'svelte';
  import { get } from './api.js';
  import { t, locale } from './i18n.svelte.js';

  // Not offered anywhere yet: it opens only with ?display=1 in the address, until
  // it can build on freely arranged dashboards.
  // The display view for a wall tablet: a large clock, the state of the services
  // and the dashboard's tiles, scaled to fill the screen without scrolling.
  // Nothing to operate until the screen is touched. The choices belong to the
  // device, so they are kept in this browser.
  let { page, boards = [], board = null, tiles = [], weatherTile = null, status = {}, onSwitch, onExit, onReconnect } = $props();

  const KEY = 'heimdeck-display';
  const DEFAULTS = { rotate: 0, night: 'off', size: 1 };
  function readPrefs() {
    try { return { ...DEFAULTS, ...JSON.parse(localStorage.getItem(KEY) || '{}') }; } catch { return { ...DEFAULTS }; }
  }
  let prefs = $state(readPrefs());
  function setPref(name, value) {
    prefs[name] = value;
    try { localStorage.setItem(KEY, JSON.stringify($state.snapshot(prefs))); } catch { /* private window */ }
  }

  let now = $state(new Date());
  let weather = $state(null);
  let controls = $state(true);
  let panel = $state(false);
  let offline = $state(false);
  let awake = $state(false);
  let fullscreen = $state(false);
  let bar = $state();
  let pages = $state([0]); // where each screenful starts, when it is more than one
  let pageIndex = $state(0);

  let time = $derived(now.toLocaleTimeString(locale(), { hour: '2-digit', minute: '2-digit' }));
  let date = $derived(now.toLocaleDateString(locale(), { weekday: 'long', day: 'numeric', month: 'long' }));

  // ---- the state of the services ----
  let checked = $derived(tiles.filter((x) => status[x.id]));
  let down = $derived(checked.filter((x) => !status[x.id].online));

  // ---- fitting the tiles to the screen ----
  const MIN_ZOOM = 0.7; // smaller cannot be read from across the room
  const MAX_ZOOM = 1.6;
  const PAGE_SECONDS = 15;
  let lastFit = 0;
  let appliedZoom = 1;
  // The largest zoom at which everything fits: at a smaller zoom more tiles fit
  // next to each other, so the height is measured rather than calculated.
  function fit() {
    if (!page || !bar) return;
    const barH = bar.offsetHeight;
    const available = innerHeight;
    page.style.paddingBottom = '';
    page.style.minHeight = '';
    page.classList.remove('centered');
    const heightAt = (z) => {
      page.style.zoom = z;
      page.style.paddingTop = `${barH / z}px`;
      return page.getBoundingClientRect().height;
    };
    let lo = MIN_ZOOM;
    let hi = MAX_ZOOM;
    if (heightAt(lo) <= available) {
      for (let i = 0; i < 7; i++) {
        const mid = (lo + hi) / 2;
        if (heightAt(mid) <= available) lo = mid;
        else hi = mid;
      }
    }
    // The chosen size works on top of what fits: larger may mean more screenfuls.
    // But a single tile is never larger than a screenful, it would be cut off.
    const floor = Math.min(lo, lo * prefs.size);
    const room = available - barH - 8;
    let zoom = lo * prefs.size;
    let height = heightAt(zoom);
    for (let i = 0; i < 8 && zoom > floor; i++) {
      const heading = page.querySelector('.group-head')?.getBoundingClientRect().height || 0;
      const tallest = Math.max(0, ...[...page.querySelectorAll('.tile')].map((e) => e.getBoundingClientRect().height));
      const needed = tallest + heading + 30 * zoom; // with the gaps around it
      if (needed <= room) break;
      zoom = Math.max(floor, zoom * Math.max(room / needed, 0.9));
      height = heightAt(zoom);
    }
    const fits = height <= available;
    appliedZoom = zoom;
    lastFit = performance.now();
    // Little on the screen: it sits in the middle instead of the top left corner.
    if (fits && available - height > available * 0.15) {
      page.classList.add('centered');
      page.style.minHeight = `${available / zoom}px`;
    }
    // Too much for one screen at a readable size: several screenfuls, which
    // take turns like a notice board. They start at a tile, never in one.
    // Room to scroll the last screenful up to the clock.
    page.style.paddingBottom = fits ? '' : `${innerHeight / zoom}px`;
    pages = fits ? [0] : findPages(barH);
    if (pageIndex >= pages.length) pageIndex = 0;
    showPage();
  }
  const parts = () => [...page.querySelectorAll('.tile, .group-head')].filter((e) => e.offsetParent);
  const boxOf = (e) => {
    const r = e.getBoundingClientRect();
    return { top: r.top + scrollY, bottom: r.bottom + scrollY, head: e.classList.contains('group-head') };
  };
  function findPages(barH) {
    const boxes = parts().map(boxOf);
    const room = innerHeight - barH - 8;
    const starts = [0];
    let from = barH;
    while (starts.length < 12) {
      const over = boxes.filter((b) => b.bottom > from + room + 1);
      if (!over.length) break;
      // The next screen starts with the first thing that did not fit,
      // and a group's heading goes along with its first tiles.
      let next = Math.min(...over.map((b) => b.top));
      const heading = boxes.find((b) => b.head && b.bottom <= next && next - b.bottom < 40);
      if (heading) next = heading.top;
      if (next <= from) break; // a single tile taller than the screen
      starts.push(Math.round(next - barH - 8));
      from = next;
    }
    return starts;
  }
  // Only what fits completely is shown: nothing cut off at the bottom, no
  // rest of the last screenful under the clock.
  function showPage() {
    if (!page) return;
    scrollTo(0, pages[pageIndex]);
    const items = page.querySelectorAll('.tile, .group-head');
    if (pages.length < 2) {
      items.forEach((e) => (e.style.visibility = ''));
      return;
    }
    // Each tile belongs to one screenful: from where it starts to where the next one does.
    const barH = bar.offsetHeight;
    const start = pageIndex ? pages[pageIndex] + barH + 8 : -1;
    const next = pageIndex + 1 < pages.length ? pages[pageIndex + 1] + barH + 8 : Infinity;
    items.forEach((e) => {
      const b = boxOf(e);
      e.style.visibility = b.top >= start - 1 && b.top < next - 1 ? '' : 'hidden';
    });
  }
  function nextPage() {
    if (pages.length < 2) return;
    page?.classList.add('fading');
    setTimeout(() => {
      pageIndex = (pageIndex + 1) % pages.length;
      showPage();
      page?.classList.remove('fading');
    }, 350);
  }
  // Tiles fill up as their data arrives, so the size is checked again – but the
  // change a fit causes itself must not start the next one.
  let timer;
  const refit = () => {
    clearTimeout(timer);
    timer = setTimeout(fit, 300);
  };
  const observer = new ResizeObserver(() => {
    if (performance.now() - lastFit > 400) refit();
  });
  $effect(() => {
    void board?.id;
    void prefs.size;
    pageIndex = 0;
    refit();
  });
  $effect(() => {
    if (page) {
      observer.disconnect();
      observer.observe(page);
    }
  });

  // ---- what runs on its own ----
  function nextBoard() {
    if (boards.length < 2) return;
    const i = boards.findIndex((b) => b.id === board?.id);
    page?.classList.add('fading');
    setTimeout(() => {
      onSwitch(boards[(i + 1) % boards.length].id);
      setTimeout(() => page?.classList.remove('fading'), 500);
    }, 350);
  }
  $effect(() => {
    const every = Number(prefs.rotate);
    if (!every) return;
    const r = setInterval(() => !panel && nextBoard(), every * 1000);
    return () => clearInterval(r);
  });

  let nightOn = $derived(prefs.night === 'always' || (prefs.night === 'night' && (now.getHours() >= 22 || now.getHours() < 6)));

  async function loadWeather() {
    if (!weatherTile) return;
    try {
      const w = (await get(`/tiles/${weatherTile.id}/widget`)).widget;
      if (w?.stats?.length) weather = w.stats[0];
    } catch { /* the clock is enough */ }
  }

  // The screen stays on (browsers allow it only over HTTPS or on localhost).
  let lock = null;
  async function keepAwake() {
    try {
      if (document.visibilityState === 'visible' && navigator.wakeLock && !lock) {
        lock = await navigator.wakeLock.request('screen');
        awake = true;
        lock.addEventListener('release', () => { lock = null; awake = false; });
      }
    } catch { awake = false; }
  }

  // A small shift now and then, so a bright picture does not burn in.
  function shift() {
    const x = Math.round(Math.random() * 8 - 4);
    const y = Math.round(Math.random() * 6 - 3);
    if (page) page.style.translate = `${x}px ${y}px`;
    if (bar) bar.style.translate = `${x}px ${y}px`;
  }

  async function ping() {
    try {
      const res = await fetch('/api/state', { cache: 'no-store' });
      if (!res.ok) throw new Error();
      if (offline) onReconnect?.();
      offline = false;
    } catch { offline = true; }
  }

  // ---- the controls appear when the screen is touched ----
  let hideTimer;
  function wake() {
    controls = true;
    document.body.classList.remove('display-idle');
    clearTimeout(hideTimer);
    hideTimer = setTimeout(() => {
      if (panel) return wake();
      controls = false;
      document.body.classList.add('display-idle');
    }, 4000);
  }
  function toggleFullscreen() {
    if (document.fullscreenElement) document.exitFullscreen?.();
    else document.documentElement.requestFullscreen?.().catch(() => {});
  }
  const onFullscreen = () => (fullscreen = !!document.fullscreenElement);
  const onVisible = () => keepAwake();
  const onKey = (e) => {
    if (e.key === 'Escape' && !document.querySelector('.backdrop')) onExit();
  };

  let tick, slow, burn, net, refresh;
  onMount(() => {
    document.body.classList.add('display-mode');
    document.documentElement.classList.add('display-mode');
    tick = setInterval(() => (now = new Date()), 1000);
    slow = setInterval(loadWeather, 10 * 60 * 1000);
    burn = setInterval(shift, 3 * 60 * 1000);
    net = setInterval(ping, 20000);
    refresh = setInterval(() => {
      if (panel) return;
      if (pages.length > 1) nextPage();
      else if (performance.now() - lastFit > 25000) fit();
    }, PAGE_SECONDS * 1000);
    addEventListener('resize', refit);
    addEventListener('pointerdown', wake);
    addEventListener('pointermove', wake);
    addEventListener('keydown', onKey);
    document.addEventListener('fullscreenchange', onFullscreen);
    document.addEventListener('visibilitychange', onVisible);
    wake();
    loadWeather();
    keepAwake();
    onFullscreen();
    refit();
  });
  onDestroy(() => {
    [tick, slow, burn, net, refresh].forEach(clearInterval);
    clearTimeout(timer);
    clearTimeout(hideTimer);
    observer.disconnect();
    if (typeof document === 'undefined') return;
    removeEventListener('resize', refit);
    removeEventListener('pointerdown', wake);
    removeEventListener('pointermove', wake);
    removeEventListener('keydown', onKey);
    document.removeEventListener('fullscreenchange', onFullscreen);
    document.removeEventListener('visibilitychange', onVisible);
    document.body.classList.remove('display-mode', 'display-idle');
    document.documentElement.classList.remove('display-mode');
    lock?.release().catch(() => {});
    if (page) {
      for (const p of ['zoom', 'paddingTop', 'paddingBottom', 'minHeight', 'translate']) page.style[p] = '';
      page.classList.remove('fading', 'centered');
      page.querySelectorAll('.tile, .group-head').forEach((e) => (e.style.visibility = ''));
    }
    if (document.fullscreenElement) document.exitFullscreen?.();
  });
</script>

<div class="dm-bar" bind:this={bar}>
  <div class="dm-when on-bg">
    <div class="dm-clock">{time}</div>
    <div class="dm-side">
      <div class="dm-date">{date}</div>
      {#if weather}<div class="weather">{weather.value} <span class="wlabel">{weather.label}</span></div>{/if}
    </div>
  </div>
  <div class="dm-right">
    {#if pages.length > 1}
      <div class="pagecount on-bg">{t('Seite {a} / {b}', { a: pageIndex + 1, b: pages.length })}</div>
    {/if}
    {#if boards.length > 1 && Number(prefs.rotate)}
      <div class="dm-dots on-bg" aria-label={t(board?.name || '')}>
        {#each boards as b (b.id)}<span class="dm-dot" class:on={b.id === board?.id}></span>{/each}
        <span class="bname">{t(board?.name || '')}</span>
      </div>
    {/if}
    {#if checked.length}
      <div class="dm-state on-bg" class:bad={down.length > 0}>
        <span class="led" aria-hidden="true"></span>
        {#if down.length}
          <span class="state-text">{t('{n} nicht erreichbar', { n: down.length })}<span class="names">{down.map((x) => x.name).join(', ')}</span></span>
        {:else}
          <span class="state-text">{t('Alle {n} Dienste erreichbar', { n: checked.length })}</span>
        {/if}
      </div>
    {/if}
  </div>
</div>

{#if offline}<div class="offline">{t('Keine Verbindung zu Heimdeck – die Daten sind nicht aktuell')}</div>{/if}
{#if nightOn}<div class="night" aria-hidden="true"></div>{/if}

<div class="controls card" class:show={controls}>
  {#if panel}
    <div class="panel">
      <label>{t('Dashboards wechseln')}
        <select value={String(prefs.rotate)} disabled={boards.length < 2} onchange={(e) => setPref('rotate', Number(e.target.value))}>
          <option value="0">{t('aus')}</option>
          <option value="30">{t('alle 30 Sekunden')}</option>
          <option value="60">{t('jede Minute')}</option>
          <option value="300">{t('alle 5 Minuten')}</option>
        </select>
      </label>
      <label>{t('Nachts dunkler')}
        <select value={prefs.night} onchange={(e) => setPref('night', e.target.value)}>
          <option value="off">{t('aus')}</option>
          <option value="night">{t('22 bis 6 Uhr')}</option>
          <option value="always">{t('immer')}</option>
        </select>
      </label>
      <label>{t('Größe')}
        <select value={String(prefs.size)} onchange={(e) => setPref('size', Number(e.target.value))}>
          <option value="0.8">{t('kleiner')}</option>
          <option value="1">{t('automatisch')}</option>
          <option value="1.25">{t('größer')}</option>
        </select>
      </label>
      <p class="note">{awake ? t('Der Bildschirm bleibt an.') : t('Der Bildschirm kann ausgehen: wach halten geht nur über HTTPS.')}</p>
    </div>
  {/if}
  <div class="row">
    <button class="ghost small" class:on={panel} onclick={() => (panel = !panel)} aria-expanded={panel}>⚙ {t('Anzeige')}</button>
    <button class="ghost small" onclick={toggleFullscreen}>{fullscreen ? '⤡' : '⛶'} {t('Vollbild')}</button>
    <button class="ghost small" onclick={onExit}>✕ {t('Beenden')}</button>
  </div>
</div>

<style>
  .dm-bar { position: fixed; inset: 0 0 auto 0; z-index: 40; display: flex; align-items: center; justify-content: space-between; gap: 20px; padding: 14px 22px 10px; pointer-events: none; }
  .pagecount { font-size: clamp(12px, 1.8vh, 15px); color: var(--muted); white-space: nowrap; font-variant-numeric: tabular-nums; }
  :global(body.has-bg) .dm-when.on-bg { padding: 6px 18px 6px 12px; }
  :global(body.has-bg) .pagecount.on-bg { padding: 4px 10px; }
  .dm-when { display: flex; align-items: center; gap: 16px; min-width: 0; text-shadow: 0 1px 12px color-mix(in srgb, var(--bg) 60%, transparent); }
  .dm-clock { font-size: clamp(40px, 8.5vh, 88px); line-height: 1; font-weight: 300; letter-spacing: -.02em; font-variant-numeric: tabular-nums; white-space: nowrap; }
  .dm-side { display: grid; gap: 2px; min-width: 0; }
  .dm-date { font-size: clamp(14px, 2.4vh, 22px); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .weather { font-size: clamp(13px, 2.1vh, 19px); color: var(--muted); white-space: nowrap; }
  .wlabel { font-size: .85em; }
  .dm-right { display: flex; align-items: center; gap: 12px; min-width: 0; }
  .dm-state { display: flex; align-items: center; gap: 10px; padding: 8px 14px; border-radius: 999px; background: var(--surface); border: 1px solid var(--border); font-size: clamp(13px, 2vh, 18px); min-width: 0; }
  .led { width: .7em; height: .7em; flex: none; border-radius: 50%; background: var(--ok); box-shadow: 0 0 10px var(--ok); }
  .dm-state.bad { border-color: var(--bad); }
  .dm-state.bad .led { background: var(--bad); box-shadow: 0 0 10px var(--bad); }
  .state-text { display: grid; min-width: 0; }
  .names { font-size: .8em; color: var(--muted); max-width: 34vw; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .dm-dots { display: flex; align-items: center; gap: 6px; font-size: 13px; }
  .dm-dot { width: 7px; height: 7px; border-radius: 50%; background: var(--muted); opacity: .45; }
  .dm-dot.on { background: var(--accent); opacity: 1; }
  .bname { margin-left: 4px; }
  .night { position: fixed; inset: 0; z-index: 45; background: rgba(0, 0, 0, .62); pointer-events: none; }
  .offline { position: fixed; left: 50%; bottom: 18px; translate: -50% 0; z-index: 46; padding: 8px 16px; border-radius: 999px; background: var(--bad); color: #fff; font-size: 14px; }
  .controls { position: fixed; right: 18px; bottom: 18px; z-index: 47; padding: 6px; display: grid; gap: 6px; background: var(--surface-solid); opacity: 0; pointer-events: none; transform: translateY(8px); transition: opacity .2s, transform .2s; }
  .controls.show { opacity: 1; pointer-events: auto; transform: none; }
  .row { display: flex; gap: 4px; }
  .row button { min-height: 40px; padding: 0 12px; white-space: nowrap; }
  .row button.on { color: var(--accent-text); }
  .panel { display: grid; gap: 10px; padding: 8px; width: 280px; }
  .panel label { display: grid; gap: 4px; font-size: 12.5px; color: var(--muted); }
  .note { margin: 0; font-size: 12px; color: var(--muted); }
  /* Nothing to scroll by hand: the screenfuls change on their own. */
  :global(html.display-mode), :global(body.display-mode) { overflow: hidden; }
  :global(body.display-idle), :global(body.display-idle *) { cursor: none !important; }
  @media (max-width: 640px) {
    .dm-bar { flex-wrap: wrap; padding: 10px 14px 6px; gap: 6px; }
    .names { max-width: 60vw; }
    .controls { left: 14px; right: 14px; bottom: 14px; }
    .panel { width: auto; }
    .row button { flex: 1; padding: 0 6px; }
  }
</style>
