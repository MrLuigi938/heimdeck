<script>
  import { get, post } from './api.js';
  import { toast } from './toast.svelte.js';
  import Icon from './Icon.svelte';
  import Widget from './Widget.svelte';
  import { tileHref, rewrite } from './network.svelte.js';
  import { t } from './i18n.svelte.js';

  // Searches the own tiles instantly and all connected services (Jellyfin,
  // Jellyseerr, Paperless …) in the background.
  let { tiles, newTab, searchEngine, hasSearchServices, local } = $props();

  let q = $state('');
  let open = $state(false);
  let results = $state([]);
  let loading = $state(false);
  let input;
  let timer;
  let seq = 0;

  const engines = {
    duckduckgo: 'https://duckduckgo.com/?q=',
    brave: 'https://search.brave.com/search?q=',
    google: 'https://www.google.com/search?q=',
    startpage: 'https://www.startpage.com/do/search?q=',
    bing: 'https://www.bing.com/search?q=',
  };

  let tileHits = $derived(
    q.trim().length === 0
      ? []
      : tiles.filter((tile) => (tile.name + ' ' + (tile.description || '')).toLowerCase().includes(q.trim().toLowerCase())).slice(0, 6),
  );

  let tileById = $derived(Object.fromEntries(tiles.map((t) => [t.id, t])));
  let noHits = $derived(results.filter((g) => !g.error && !g.items?.length).map((g) => g.name));

  function onInput() {
    open = true;
    clearTimeout(timer);
    const term = q.trim();
    if (term.length < 2 || !hasSearchServices) {
      results = [];
      loading = false;
      return;
    }
    loading = true;
    timer = setTimeout(async () => {
      const my = ++seq;
      try {
        const res = await get('/search?q=' + encodeURIComponent(term));
        if (my === seq) results = res;
      } catch {
        if (my === seq) results = [];
      }
      if (my === seq) loading = false;
    }, 350);
  }

  function webSearch() {
    const base = engines[searchEngine] || engines.duckduckgo;
    window.open(base + encodeURIComponent(q.trim()), newTab ? '_blank' : '_self');
  }

  function onKey(e) {
    if (e.key === 'Escape') {
      q = '';
      open = false;
      input.blur();
    } else if (e.key === 'Enter' && q.trim()) {
      if (tileHits.length) window.open(tileHref(tileHits[0], local), newTab ? '_blank' : '_self');
      else webSearch();
    }
  }

  function globalKey(e) {
    const tag = document.activeElement?.tagName;
    if (e.key === '/' && tag !== 'INPUT' && tag !== 'TEXTAREA' && tag !== 'SELECT') {
      e.preventDefault();
      input.focus();
    }
  }

  function makeAction(tileId) {
    return async (a) => {
      try {
        await post(`/tiles/${tileId}/action`, { action: a.id, params: a.params || {} });
        toast(t('{action}: erledigt', { action: a.label }));
        onInput();
      } catch (e) {
        toast(e.message, 'bad');
      }
    };
  }
</script>

<svelte:window onkeydown={globalKey} />

<div class="search" onfocusout={(e) => !e.currentTarget.contains(e.relatedTarget) && setTimeout(() => (open = false), 150)}>
  <svg class="glass" viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><circle cx="11" cy="11" r="7" /><path d="m20 20-3.5-3.5" /></svg>
  <input
    bind:this={input}
    bind:value={q}
    oninput={onInput}
    onfocus={() => (open = q.length > 0)}
    onkeydown={onKey}
    placeholder={hasSearchServices ? t('Suchen in Diensten, Filmen, Serien, Dokumenten …') + '  ( / )' : t('Dienste suchen oder im Web suchen …') + '  ( / )'}
    aria-label={t('Suche')}
  />
  {#if open && q.trim()}
    <div class="panel card" tabindex="-1">
      {#if tileHits.length}
        <div class="section">{t('Kacheln')}</div>
        {#each tileHits as hit}
          <a class="hit" href={tileHref(hit, local)} target={newTab ? '_blank' : undefined} rel="noopener noreferrer">
            <Icon icon={hit.icon} name={hit.name} size={24} /> <span>{hit.name}</span>
          </a>
        {/each}
      {/if}

      {#each results as g}
        {@const tile = tileById[g.tileId]}
        {#if g.items?.length || g.error}
          <div class="section"><Icon icon={g.icon} name={g.name} size={16} /> {g.name}</div>
          {#if g.error}
            <div class="err">{g.error}</div>
          {:else}
            <div class="remote">
              {#each g.items as it}
                <div class="remote-item">
                  {#if it.image}<img src={rewrite(it.image, tile, local)} alt="" loading="lazy" onerror={(e) => (e.currentTarget.style.visibility = 'hidden')} />{/if}
                  <div class="grow"><Widget widget={{ items: [it] }} onAction={makeAction(g.tileId)} {newTab} compact={false} link={(u) => rewrite(u, tile, local)} /></div>
                </div>
              {/each}
            </div>
          {/if}
        {/if}
      {/each}

      {#if loading}
        <div class="section muted">{t('Suche in deinen Diensten …')}</div>
      {:else if noHits.length}
        <div class="none">{t('Keine Treffer in')}: {noHits.join(', ')}</div>
      {/if}

      <button class="hit web" onclick={webSearch}>🌐 {t('Im Web suchen: „{q}“', { q: q.trim() })}</button>
    </div>
  {/if}
</div>

<style>
  .search { position: relative; width: 100%; max-width: 620px; }
  .glass { position: absolute; left: 14px; top: 50%; transform: translateY(-50%); color: var(--muted); pointer-events: none; }
  input { padding: 12px 14px 12px 40px; border-radius: 14px; background: var(--surface); backdrop-filter: blur(14px); border-color: var(--border); }
  .panel {
    position: absolute; top: calc(100% + 6px); left: 0; right: 0; z-index: 40; background: var(--surface-solid);
    padding: 8px; max-height: min(70vh, 640px); overflow-y: auto; display: grid; gap: 2px;
  }
  .section { display: flex; gap: 6px; align-items: center; font-size: 11.5px; text-transform: uppercase; letter-spacing: .06em; color: var(--muted); padding: 10px 8px 4px; }
  .hit { display: flex; gap: 10px; align-items: center; padding: 8px; border-radius: 10px; border: 0; background: none; text-align: left; width: 100%; }
  .hit:hover, .hit:focus { background: var(--surface-2); }
  .web { color: var(--muted); margin-top: 4px; border-top: 1px solid var(--border); border-radius: 0 0 10px 10px; }
  .remote { padding: 0 8px; }
  .remote-item { display: flex; gap: 10px; align-items: center; }
  .remote-item img { width: 32px; height: 48px; object-fit: cover; border-radius: 4px; flex: none; }
  .grow { flex: 1; min-width: 0; }
  .none { font-size: 12.5px; color: var(--muted); padding: 8px 8px 2px; }
  .err { font-size: 12.5px; color: var(--bad); padding: 0 8px 6px; }
</style>
