<script>
  // Background of the dashboard (or of one dashboard): uploaded pictures,
  // picture addresses and photo albums from Immich or PhotoPrism. With more
  // than one picture they take turns (see backgrounds.js). Stored as one
  // entry per line – a single address from before stays valid.
  import { get } from './api.js';
  import { toast } from './toast.svelte.js';
  import { t } from './i18n.svelte.js';
  import { uploadImage } from './upload.js';
  import { parseBackgrounds, joinBackgrounds } from './backgrounds.js';

  let { value = '', photoTiles = [], onChange, compact = false } = $props();

  let entries = $derived(parseBackgrounds(value));
  let files = $state();
  let uploading = $state(0);
  let adding = $state(''); // '' | 'url' | 'album'
  let url = $state('');
  let albumTile = $state(0);
  let albums = $state(null);
  let album = $state('');
  let checking = $state(false);

  const save = (list) => onChange?.(joinBackgrounds(list));
  const remove = (i) => save(entries.filter((_, j) => j !== i));

  async function upload(e) {
    const chosen = [...(e.currentTarget.files || [])];
    e.currentTarget.value = '';
    if (!chosen.length) return;
    uploading = chosen.length;
    const added = [];
    for (const f of chosen) {
      try {
        added.push({ kind: 'url', url: (await uploadImage(f, 'background')).url });
      } catch (err) {
        toast(`${f.name}: ${err.message}`, 'bad');
      }
      uploading--;
    }
    if (added.length) save([...entries, ...added]);
  }

  function addUrl() {
    const u = url.trim();
    if (!/^https?:\/\//i.test(u)) {
      toast(t('Bitte eine vollständige Adresse mit https:// angeben'), 'bad');
      return;
    }
    save([...entries, { kind: 'url', url: u }]);
    url = '';
    adding = '';
  }

  async function loadAlbums() {
    albums = null;
    album = '';
    if (!albumTile) return;
    try {
      albums = await get(`/tiles/${albumTile}/albums`);
    } catch (err) {
      toast(err.message, 'bad');
      albums = [];
    }
  }

  // Checked before it is added: a background without pictures would just stay empty.
  async function addAlbum() {
    const a = albums?.find((x) => x.id === album);
    if (!a) return;
    checking = true;
    try {
      const { images = [] } = await get(`/tiles/${albumTile}/albums/${encodeURIComponent(a.id)}`);
      if (!images.length) {
        toast(t('In diesem Album sind keine Bilder (Videos werden nicht gezeigt)'), 'bad');
        return;
      }
    } catch (err) {
      toast(err.message, 'bad');
      return;
    } finally {
      checking = false;
    }
    save([...entries, { kind: 'album', tile: albumTile, album: a.id, name: a.name }]);
    adding = '';
    album = '';
  }
</script>

<div class="bgpick" class:compact>
  {#if entries.length}
    <div class="thumbs">
      {#each entries as e, i (i)}
        <div class="thumb" title={e.kind === 'album' ? e.name : e.url}>
          {#if e.kind === 'album'}
            <span class="album">📷<small>{e.name}</small></span>
          {:else}
            <img src={e.url} alt="" loading="lazy" />
          {/if}
          <button type="button" class="ghost x" onclick={() => remove(i)} aria-label={t('Entfernen')} title={t('Entfernen')}>✕</button>
        </div>
      {/each}
    </div>
  {/if}
  <div class="row wrap">
    <button type="button" class="small" disabled={uploading > 0} onclick={() => files.click()}>⬆ {uploading ? t('Lade hoch … ({n})', { n: uploading }) : t('Bilder hochladen')}</button>
    <input type="file" accept="image/*" multiple hidden bind:this={files} onchange={upload} />
    <button type="button" class="small" class:on={adding === 'url'} onclick={() => (adding = adding === 'url' ? '' : 'url')}>🔗 {t('Adresse')}</button>
    {#if photoTiles.length}
      <button type="button" class="small" class:on={adding === 'album'} onclick={() => (adding = adding === 'album' ? '' : 'album')}>📷 {t('Album')}</button>
    {/if}
  </div>
  {#if adding === 'url'}
    <div class="row">
      <input bind:value={url} placeholder="https://…/wallpaper.jpg" onkeydown={(e) => e.key === 'Enter' && (e.preventDefault(), addUrl())} />
      <button type="button" class="small primary" onclick={addUrl}>{t('Hinzufügen')}</button>
    </div>
  {:else if adding === 'album'}
    <div class="row wrap">
      <select bind:value={albumTile} onchange={loadAlbums} aria-label={t('Fotodienst')}>
        <option value={0}>{t('Fotodienst wählen …')}</option>
        {#each photoTiles as pt (pt.id)}<option value={pt.id}>{pt.name}</option>{/each}
      </select>
      {#if albumTile}
        <select bind:value={album} disabled={!albums} aria-label={t('Album')}>
          <option value="">{!albums ? t('Lade …') : albums.length ? t('Album wählen …') : t('Keine Alben gefunden')}</option>
          {#each albums || [] as a (a.id)}<option value={a.id}>{a.name} ({a.value})</option>{/each}
        </select>
        <button type="button" class="small primary" disabled={!album || checking} onclick={addAlbum}>{t('Hinzufügen')}</button>
      {/if}
    </div>
  {/if}
  <span class="help">{t('Große Bilder werden beim Hochladen verkleinert. Mehrere Bilder oder ein Album wechseln sich ab.')}</span>
  {#if !photoTiles.length}
    <span class="help">{t('Für Fotoalben braucht es eine eigene Immich- oder PhotoPrism-Kachel mit API-Key.')}</span>
  {/if}
</div>

<style>
  .bgpick { display: grid; gap: 8px; }
  .thumbs { display: flex; flex-wrap: wrap; gap: 8px; }
  .thumb { position: relative; width: 96px; height: 60px; border-radius: var(--r-sm); overflow: hidden; border: 1px solid var(--border); background: var(--surface-2); }
  .compact .thumb { width: 72px; height: 45px; }
  .thumb img { width: 100%; height: 100%; object-fit: cover; display: block; }
  .album { display: grid; place-items: center; height: 100%; font-size: 18px; line-height: 1.1; text-align: center; padding: 2px; }
  .album small { font-size: 10.5px; color: var(--muted); max-width: 100%; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .x { position: absolute; top: 2px; right: 2px; width: 22px; height: 22px; padding: 0; justify-content: center; font-size: 11px; background: rgba(0, 0, 0, .55) !important; /* on a picture, in every theme */ color: #fff; border-radius: 50%; }
  .row { display: flex; gap: 8px; align-items: center; }
  .row input { flex: 1; min-width: 0; }
  .wrap { flex-wrap: wrap; }
  .on { border-color: var(--accent); color: var(--accent-text); }
</style>
