<script>
  // Shows an icon from the dashboard-icons collection (by name), a URL, an
  // emoji, or the first letter of the name as fallback. The letter stays
  // visible until the image has loaded, so slow or offline CDNs never leave a gap.
  let { icon = '', name = '', size = 40 } = $props();

  const CDN = 'https://cdn.jsdelivr.net/gh/homarr-labs/dashboard-icons';

  // Only a single emoji (or one or two characters) is drawn as text; longer
  // text like "Paperless NGX" or "sonarr.png" is turned into an icon name,
  // so it can never overflow the tile.
  const isEmoji = (s) => /\p{Extended_Pictographic}/u.test(s) && [...new Intl.Segmenter().segment(s)].length <= 2;
  let sources = $derived.by(() => {
    const i = (icon || '').trim();
    if (!i || isEmoji(i)) return [];
    if (/^(https?:)?\/\//.test(i) || i.startsWith('/') || i.startsWith('data:')) return [i];
    const slug = i.toLowerCase().replace(/\.(svg|png|webp|jpe?g)$/, '').replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '');
    if (!slug) return [];
    return [`${CDN}/svg/${slug}.svg`, `${CDN}/png/${slug}.png`, `${CDN}/webp/${slug}.webp`];
  });
  let emoji = $derived(icon && isEmoji(icon.trim()) ? icon.trim() : '');

  // Load state is remembered per URL instead of being reset in an effect:
  // a cached image can finish loading before an effect runs, which used to
  // leave the placeholder letter visible for good.
  let failed = $state({});
  let loadedSrc = $state('');
  let src = $derived(sources.find((s) => !failed[s]) || '');
  let loaded = $derived(src !== '' && loadedSrc === src);

  // Catches images that were already complete when they were inserted.
  function checkComplete(img) {
    if (img.complete && img.naturalWidth > 0) loadedSrc = img.getAttribute('src');
  }

  const palette = ['#5b8cff', '#3ecf8e', '#f5b942', '#f2555a', '#a78bfa', '#22c3d6', '#fb7d4b'];
  let color = $derived(palette[[...(name || '?')].reduce((a, c) => a + c.charCodeAt(0), 0) % palette.length]);
</script>

<div class="icon" style="width:{size}px;height:{size}px">
  {#if emoji}
    <span style="font-size:{size * 0.7}px">{emoji}</span>
  {:else}
    {#if !loaded}
      <span class="letter" style="background:{color};font-size:{size * 0.45}px">{(name || '?').trim().charAt(0).toUpperCase()}</span>
    {/if}
    {#if src}
      {#key src}
        <img {src} alt="" class:hidden={!loaded} use:checkComplete
          onload={() => (loadedSrc = src)} onerror={() => (failed[src] = true)} />
      {/key}
    {/if}
  {/if}
</div>

<style>
  .icon { display: grid; place-items: center; flex: none; overflow: hidden; }
  .icon span { line-height: 1; }
  .icon > * { grid-area: 1 / 1; }
  img { width: 100%; height: 100%; object-fit: contain; }
  img.hidden { visibility: hidden; }
  .letter { width: 100%; height: 100%; border-radius: 25%; display: grid; place-items: center; color: #fff; font-weight: 700; }
</style>
