<script>
  import { onDestroy } from 'svelte';
  import { i18n, t } from './i18n.svelte.js';

  // A camera tile: a still picture (refreshed as set on the tile), ▶ for the
  // live stream and the latest events. Pictures come either as the camera's
  // own JPEG or – for cameras with only RTSP – as one H.264/H.265 key frame in
  // a tiny MP4, which the browser decodes and turns into a picture here.
  let { tileId, view } = $props();

  let still = $state(''); // object URL of the picture shown
  let stillAt = $state(0);
  let busy = $state(false);
  let error = $state('');
  let live = $state(false);
  let liveWaiting = $state(false);
  let shown = $state(null); // an event whose picture is shown instead
  let now = $state(Date.now());
  let box = $state();
  let video = $state();

  const MS = typeof window !== 'undefined' ? window.ManagedMediaSource || window.MediaSource : null;
  const base = () => `/api/tiles/${tileId}/camera`;

  async function fetchPicture(url) {
    const res = await fetch(url, { headers: { 'X-Lang': i18n.lang }, credentials: 'same-origin', cache: 'no-store' });
    if (!res.ok) {
      let msg = t('Fehler {code}', { code: res.status });
      try { msg = (await res.json()).error || msg; } catch { /* not JSON */ }
      throw new Error(msg);
    }
    const type = res.headers.get('Content-Type') || '';
    const data = await res.arrayBuffer();
    if (type.startsWith('image/')) return URL.createObjectURL(new Blob([data], { type }));
    return URL.createObjectURL(await frameToImage(data, res.headers.get('X-Codec') || 'avc1.42e01e'));
  }

  // Decodes the key frame in an MP4 with the browser's video decoder and
  // returns it as a JPEG.
  function frameToImage(data, codec) {
    return new Promise((resolve, reject) => {
      const type = `video/mp4; codecs="${codec}"`;
      if (!MS || !MS.isTypeSupported(type)) return reject(new Error(unsupported(codec)));
      const v = document.createElement('video');
      v.muted = true;
      v.playsInline = true;
      v.disableRemotePlayback = true;
      const ms = new MS();
      const src = URL.createObjectURL(ms);
      const done = (err, blob) => {
        clearTimeout(timer);
        v.removeAttribute('src');
        v.load();
        URL.revokeObjectURL(src);
        err ? reject(err) : resolve(blob);
      };
      const timer = setTimeout(() => done(new Error(t('Das Bild der Kamera ließ sich nicht anzeigen'))), 8000);
      ms.addEventListener('sourceopen', () => {
        try {
          const sb = ms.addSourceBuffer(type);
          sb.addEventListener('updateend', () => {
            try { ms.endOfStream(); } catch { /* already ended */ }
            if (sb.buffered.length) v.currentTime = sb.buffered.start(0);
          }, { once: true });
          sb.appendBuffer(data);
        } catch (e) {
          done(e);
        }
      }, { once: true });
      const grab = () => {
        if (!v.videoWidth) return;
        const c = document.createElement('canvas');
        c.width = v.videoWidth;
        c.height = v.videoHeight;
        c.getContext('2d').drawImage(v, 0, 0);
        c.toBlob((b) => (b ? done(null, b) : done(new Error('canvas'))), 'image/jpeg', 0.88);
      };
      v.addEventListener('loadeddata', grab, { once: true });
      v.addEventListener('seeked', grab, { once: true });
      v.src = src;
    });
  }

  function unsupported(codec) {
    return codec.startsWith('hvc1') || codec.startsWith('hev1')
      ? t('Dieser Browser kann H.265 nicht abspielen – im Kamera-Menü den Substream (H.264) wählen oder Chrome, Edge oder Safari nehmen.')
      : t('Dieser Browser kann das Video der Kamera nicht abspielen.');
  }

  function setStill(url) {
    if (still) URL.revokeObjectURL(still);
    still = url;
    stillAt = Date.now();
  }

  async function loadStill() {
    if (busy || live) return;
    busy = true;
    try {
      setStill(await fetchPicture(base() + '/still'));
      error = '';
    } catch (e) {
      error = e.message;
    } finally {
      busy = false;
    }
  }

  // --- live stream ---
  let abort = null;
  let ms = null;

  async function startLive() {
    if (live) return;
    shown = null;
    error = '';
    live = true;
    liveWaiting = true;
    abort = new AbortController();
    let res;
    try {
      res = await fetch(base() + '/live', { headers: { 'X-Lang': i18n.lang }, credentials: 'same-origin', signal: abort.signal });
      if (!res.ok) {
        let msg = t('Fehler {code}', { code: res.status });
        try { msg = (await res.json()).error || msg; } catch { /* not JSON */ }
        throw new Error(msg);
      }
      const codec = res.headers.get('X-Codec') || 'avc1.42e01e';
      const type = `video/mp4; codecs="${codec}"`;
      if (!MS || !MS.isTypeSupported(type)) throw new Error(unsupported(codec));
      ms = new MS();
      video.disableRemotePlayback = true;
      video.src = URL.createObjectURL(ms);
      await new Promise((r) => ms.addEventListener('sourceopen', r, { once: true }));
      const sb = ms.addSourceBuffer(type);
      sb.mode = 'segments';
      const queue = [];
      let first = true;
      const pump = () => {
        if (sb.updating || !queue.length || ms.readyState !== 'open') return;
        // Keep about 20 s behind the live edge, drop the rest.
        if (sb.buffered.length && video.currentTime - sb.buffered.start(0) > 30) {
          sb.remove(sb.buffered.start(0), video.currentTime - 20);
          return;
        }
        sb.appendBuffer(queue.shift());
      };
      sb.addEventListener('updateend', () => {
        if (sb.buffered.length) {
          const end = sb.buffered.end(sb.buffered.length - 1);
          if (first) {
            first = false;
            video.currentTime = sb.buffered.start(0);
            video.play().catch(() => {});
          } else if (end - video.currentTime > 1.5) {
            video.currentTime = end - 0.3; // stay close to the live picture
          }
          if (liveWaiting && video.readyState >= 2) liveWaiting = false;
        }
        pump();
      });
      video.addEventListener('playing', () => (liveWaiting = false), { once: true });
      const reader = res.body.getReader();
      for (;;) {
        const { value, done } = await reader.read();
        if (done) break;
        queue.push(value);
        pump();
      }
      if (live) error = t('Die Kamera hat die Verbindung beendet');
    } catch (e) {
      if (e.name !== 'AbortError') error = e.message;
    }
    stopLive(true);
  }

  // stopLive ends the stream; the last picture stays as the still.
  function stopLive(keepPicture = true) {
    if (!live) return;
    if (keepPicture && video?.videoWidth) {
      const c = document.createElement('canvas');
      c.width = video.videoWidth;
      c.height = video.videoHeight;
      c.getContext('2d').drawImage(video, 0, 0);
      c.toBlob((b) => b && setStill(URL.createObjectURL(b)), 'image/jpeg', 0.88);
    }
    live = false;
    liveWaiting = false;
    abort?.abort();
    abort = null;
    if (video) {
      video.pause();
      video.removeAttribute('src');
      video.load();
    }
    ms = null;
  }

  async function showEvent(e) {
    if (!e.image) return;
    if (shown?.id === e.id) return (shown = null);
    stopLive();
    try {
      shown = { ...e, url: await fetchPicture(`${base()}/events/${e.id}`) };
    } catch (err) {
      error = err.message;
    }
  }

  function fullscreen() {
    const el = box;
    (el.requestFullscreen || el.webkitRequestFullscreen)?.call(el);
  }

  // --- refreshing ---
  let timer = null;
  function schedule() {
    clearInterval(timer);
    const every = Number(view.still);
    if (every > 0) {
      timer = setInterval(() => {
        if (document.visibilityState === 'visible') loadStill();
      }, every * 1000);
    }
  }
  const visible = () => {
    // A tab in the background does not keep the camera awake.
    if (document.visibilityState === 'hidden') stopLive();
  };
  const clock = setInterval(() => (now = Date.now()), 5000);

  let started = false;
  $effect(() => {
    if (!started && view) {
      started = true;
      if (view.still !== 'click') loadStill();
      schedule();
      document.addEventListener('visibilitychange', visible);
    }
  });
  onDestroy(() => {
    clearInterval(timer);
    clearInterval(clock);
    if (typeof document !== 'undefined') document.removeEventListener('visibilitychange', visible);
    stopLive(false);
    if (still) URL.revokeObjectURL(still);
    if (shown?.url) URL.revokeObjectURL(shown.url);
  });

  const icons = { Bewegung: '🏃', Motion: '🏃', Person: '🧍', Fahrzeug: '🚗', Vehicle: '🚗', Tier: '🐾', Animal: '🐾', Klingel: '🔔', Doorbell: '🔔', Sabotage: '⚠️', Tampering: '⚠️', 'Bereich betreten': '🚧', 'Area entered': '🚧' };
  const icon = (k) => icons[k] || '•';
  function ago(iso) {
    const s = Math.max(0, Math.round((now - new Date(iso).getTime()) / 1000));
    if (s < 60) return t('gerade eben');
    if (s < 3600) return t('vor {n} Min.', { n: Math.round(s / 60) });
    if (s < 86400) return new Date(iso).toLocaleTimeString(i18n.lang, { hour: '2-digit', minute: '2-digit' });
    return new Date(iso).toLocaleString(i18n.lang, { weekday: 'short', hour: '2-digit', minute: '2-digit' });
  }
  function age(ms) {
    const s = Math.round((now - ms) / 1000);
    return s < 60 ? t('Bild von gerade eben') : t('Bild von vor {n} Min.', { n: Math.round(s / 60) });
  }

  let events = $derived(view?.events || []);
  let latest = $derived(events[0]);
  let fresh = $derived(latest && now - new Date(latest.at).getTime() < 10 * 60 * 1000);
</script>

<div class="cam">
  <div class="screen" bind:this={box}>
    <!-- svelte-ignore a11y_media_has_caption -->
    <video bind:this={video} muted playsinline class:on={live}></video>
    {#if !live}
      {#if shown}
        <img src={shown.url} alt={shown.kind} />
      {:else if still}
        <img src={still} alt={t('Kamerabild')} />
      {:else if busy}
        <div class="placeholder"><span class="spin"></span></div>
      {:else}
        <button class="placeholder load" onclick={loadStill}>📷 {t('Bild laden')}</button>
      {/if}
    {/if}

    {#if liveWaiting}<div class="placeholder over"><span class="spin"></span></div>{/if}

    {#if shown}
      <div class="tag top">{icon(shown.kind)} {shown.kind} · {ago(shown.at)}</div>
      <button class="chip close" onclick={() => (shown = null)} aria-label={t('Schließen')}>✕</button>
    {:else if latest && fresh && !live}
      <button class="tag top event" onclick={() => showEvent(latest)} title={t('Bild des Ereignisses zeigen')}>{icon(latest.kind)} {latest.kind} · {ago(latest.at)}</button>
    {/if}
    {#if live}<div class="tag top live">● {t('Live')}</div>{/if}

    <div class="controls">
      {#if !live && still && !shown}<span class="age">{age(stillAt)}</span>{/if}
      <span class="grow"></span>
      {#if !live && !shown}
        <button class="chip" onclick={loadStill} disabled={busy} title={t('Bild aktualisieren')} aria-label={t('Bild aktualisieren')}>⟳</button>
      {/if}
      {#if view.hasLive}
        {#if live}
          <button class="chip" onclick={() => stopLive()} title={t('Livebild beenden')} aria-label={t('Livebild beenden')}>■</button>
        {:else}
          <button class="chip play" onclick={startLive} title={t('Livebild')} aria-label={t('Livebild')}>▶</button>
        {/if}
      {/if}
      <button class="chip" onclick={fullscreen} title={t('Vollbild')} aria-label={t('Vollbild')}>⛶</button>
    </div>
  </div>

  {#if error}<div class="error">{error}</div>{/if}

  {#if view.list > 0 && events.length}
    <ul class="events">
      {#each events.slice(0, view.list) as e (e.id)}
        <li>
          <button disabled={!e.image} class:active={shown?.id === e.id} onclick={() => showEvent(e)}>
            <span class="ic">{icon(e.kind)}</span><span class="kind">{e.kind}</span><span class="when">{ago(e.at)}</span>
          </button>
        </li>
      {/each}
    </ul>
  {:else if view.detect && !events.length}
    <div class="quiet">{view.detect === 'stream' ? t('Bewegungserkennung aktiv – noch keine Ereignisse') : t('Ereignisse der Kamera aktiv – noch keine')}</div>
  {/if}
</div>

<style>
  .cam { display: grid; gap: 8px; min-width: 0; }
  .screen { position: relative; aspect-ratio: 16 / 9; background: #000; border-radius: var(--r-sm); overflow: hidden; }
  .screen:fullscreen { border-radius: 0; aspect-ratio: auto; }
  .screen img, .screen video { position: absolute; inset: 0; width: 100%; height: 100%; object-fit: contain; display: block; }
  .screen video { visibility: hidden; }
  .screen video.on { visibility: visible; }
  .placeholder { position: absolute; inset: 0; display: grid; place-items: center; color: #ddd; background: #111; border: 0; border-radius: 0; font-size: 13px; }
  .placeholder.over { background: transparent; }
  .placeholder.load:hover { color: #fff; background: #1a1a1a; }
  .spin { width: 22px; height: 22px; border: 2px solid rgba(255, 255, 255, .25); border-top-color: #fff; border-radius: 50%; animation: spin 0.9s linear infinite; }
  @keyframes spin { to { transform: rotate(360deg); } }
  @media (prefers-reduced-motion: reduce) { .spin { animation-duration: 3s; } }
  .tag { position: absolute; left: 8px; font-size: 12px; color: #fff; background: rgba(0, 0, 0, .55); padding: 3px 8px; border-radius: 999px; border: 0; backdrop-filter: blur(4px); white-space: nowrap; max-width: calc(100% - 60px); overflow: hidden; text-overflow: ellipsis; }
  .tag.top { top: 8px; }
  .tag.event { cursor: pointer; background: color-mix(in srgb, var(--warn) 75%, rgba(0, 0, 0, .4)); color: #111; font-weight: 600; }
  .tag.live { color: #fff; background: rgba(220, 38, 38, .85); font-weight: 600; }
  .controls { position: absolute; inset: auto 0 0 0; display: flex; align-items: center; gap: 6px; padding: 18px 8px 8px; background: linear-gradient(transparent, rgba(0, 0, 0, .55)); }
  .grow { flex: 1; }
  .age { font-size: 11.5px; color: rgba(255, 255, 255, .85); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .chip { width: 30px; height: 30px; padding: 0; display: grid; place-items: center; border-radius: 50%; border: 0; color: #fff; background: rgba(0, 0, 0, .5); font-size: 14px; line-height: 1; }
  .chip:hover:not(:disabled) { background: rgba(0, 0, 0, .75); }
  .chip.play { width: 38px; height: 38px; font-size: 16px; background: var(--accent); color: var(--on-accent, #fff); }
  .chip.close { position: absolute; top: 6px; right: 6px; }
  .error { font-size: 12.5px; color: var(--bad); line-height: 1.4; }
  .events { list-style: none; margin: 0; padding: 0; display: grid; gap: 2px; }
  .events button { width: 100%; display: flex; gap: 8px; align-items: center; padding: 5px 6px; border: 0; background: none; border-radius: var(--r-xs); font-size: 13px; text-align: left; color: inherit; }
  .events button:hover:not(:disabled), .events button.active { background: var(--surface-2); }
  .events button:disabled { opacity: 1; cursor: default; }
  .ic { width: 18px; text-align: center; flex: none; }
  .kind { flex: 1; min-width: 0; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .when { font-size: 12px; color: var(--muted); white-space: nowrap; }
  .quiet { font-size: 12px; color: var(--muted); }
</style>
