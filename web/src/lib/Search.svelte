<script>
  import { get, post, faviconUrl } from './api.js';
  import { toast } from './toast.svelte.js';
  import Icon from './Icon.svelte';
  import Widget from './Widget.svelte';
  import SearchWindow from './SearchWindow.svelte';
  import { tileHref, rewrite } from './network.svelte.js';
  import { t } from './i18n.svelte.js';

  // Searches the own tiles instantly and all connected services (Jellyfin,
  // Jellyseerr, Paperless …) in the background. Three ways to show the results
  // (setting "searchView"): a large window with filters and a preview (default),
  // one column per service below the field, or the compact list below the field.
  // shortcut is only shown in the placeholder; the dashboard handles the key and calls focus().
  // jumpTop: the shortcut scrolls up to the field instead of showing the window in the middle.
  // history: remember recent searches in this browser (setting "searchHistory").
  let { tiles, defs = {}, view = 'window', jumpTop = false, history = true, newTab, searchEngine, searchUrl, hasSearchServices, local, boardId, shortcut = '/' } = $props();

  // The search window opens right under the field when the field is in view
  // (anchored, you type in the field itself), otherwise in the middle of the
  // screen – or, with jumpTop, the page scrolls up first. Phones always get
  // the full-screen window.
  const phone = () => matchMedia('(max-width: 640px)').matches;
  function fieldInView() {
    const r = input.getBoundingClientRect();
    return r.bottom > 0 && r.top < innerHeight;
  }
  function openAnchored() {
    anchored = true;
    windowOpen = true;
    input.focus({ preventScroll: true });
  }
  export function focus() {
    if (view !== 'window') return input.focus();
    if (phone()) {
      anchored = false;
      windowOpen = true;
    } else if (fieldInView()) {
      openAnchored();
    } else if (jumpTop) {
      // Without animation: the window is placed under the field once it is back in view.
      scrollTo({ top: 0, behavior: 'instant' });
      requestAnimationFrame(openAnchored);
    } else {
      anchored = false;
      windowOpen = true;
    }
  }

  let q = $state('');
  let open = $state(false);
  let windowOpen = $state(false);
  let anchored = $state(false);
  let win;
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
    ecosia: 'https://www.ecosia.org/search?q=',
    qwant: 'https://www.qwant.com/?q=',
    kagi: 'https://kagi.com/search?q=',
  };

  // "!jelly severance" searches only in the services whose tile name starts with "jelly".
  let parsed = $derived.by(() => {
    const m = q.match(/^!(\S+)\s*(.*)$/);
    return m ? { prefix: m[1].toLowerCase(), term: m[2].trim() } : { prefix: '', term: q.trim() };
  });
  const inPrefix = (tile) => !parsed.prefix || (tile?.name || '').toLowerCase().startsWith(parsed.prefix);

  let tileHits = $derived(
    parsed.term.length === 0 || parsed.prefix
      ? []
      : tiles.filter((tile) => (tile.name + ' ' + (tile.description || '')).toLowerCase().includes(parsed.term.toLowerCase())).slice(0, 6),
  );

  let tileById = $derived(Object.fromEntries(tiles.map((t) => [t.id, t])));
  let groups = $derived(results.filter((g) => inPrefix(tileById[g.tileId])));
  let noHits = $derived(groups.filter((g) => !g.error && !g.items?.length).map((g) => g.name));

  function onInput() {
    open = true;
    clearTimeout(timer);
    const term = parsed.term;
    if (term.length < 2 || !hasSearchServices) {
      results = [];
      loading = false;
      return;
    }
    loading = true;
    timer = setTimeout(async () => {
      const my = ++seq;
      try {
        const res = await get('/search?q=' + encodeURIComponent(term) + (boardId ? '&board=' + boardId : ''));
        if (my === seq) results = res;
      } catch {
        if (my === seq) results = [];
      }
      if (my === seq) loading = false;
    }, 350);
  }

  // Recent searches (this browser only), shown in the window while the field is empty.
  const RECENT = 'heimdeck-recent-searches';
  let recent = $state(readRecent());
  function readRecent() {
    try {
      return JSON.parse(localStorage.getItem(RECENT) || '[]');
    } catch {
      return [];
    }
  }
  function storeRecent(list) {
    recent = list;
    try {
      if (list.length) localStorage.setItem(RECENT, JSON.stringify(list));
      else localStorage.removeItem(RECENT);
    } catch {
      /* private mode */
    }
  }
  function remember() {
    const term = q.trim();
    if (!history || term.length < 2) return;
    storeRecent([term, ...recent.filter((r) => r !== term)].slice(0, 6));
  }
  const forget = (term) => storeRecent(recent.filter((r) => r !== term));
  // History turned off in the settings: what was remembered is deleted too.
  $effect(() => {
    if (!history && recent.length) storeRecent([]);
  });

  function webSearch() {
    remember();
    const term = encodeURIComponent(parsed.term || q.trim());
    // A custom engine (e.g. your own SearXNG) marks the search term with {q} or %s.
    const custom = searchEngine === 'custom' && /^https?:\/\//i.test(searchUrl || '') ? searchUrl : '';
    const href = custom
      ? (/\{q\}|%s/.test(custom) ? custom.replace(/\{q\}|%s/g, term) : custom + term)
      : (engines[searchEngine] || engines.duckduckgo) + term;
    window.open(href, newTab ? '_blank' : '_self');
    reset();
  }

  function openUrl(url) {
    remember();
    window.open(url, newTab ? '_blank' : '_self');
    reset();
  }

  // Empties the search, so the next one can start right away – after an
  // action, a web search or opening a service.
  function reset({ focus = false } = {}) {
    clearTimeout(timer);
    seq++;
    q = '';
    results = [];
    loading = false;
    open = false;
    if (!focus) windowOpen = false;
    if (focus) input?.focus();
    else input?.blur();
  }

  function onKey(e) {
    if (e.key === 'Escape') {
      reset();
    } else if (e.key === 'Enter' && q.trim()) {
      if (tileHits.length) openUrl(tileHref(tileHits[0], local));
      else webSearch();
    }
  }

  // Coming back with the browser's Back button restores the page as it was,
  // including the typed text: start empty instead.
  function onPageShow(e) {
    if (e.persisted) reset();
  }

  function makeAction(tileId) {
    return async (a) => {
      try {
        await post(`/tiles/${tileId}/action`, { action: a.id, params: a.params || {} });
        toast(t('{action}: erledigt', { action: a.label }));
        remember();
        reset({ focus: true });
      } catch (e) {
        toast(e.message, 'bad');
      }
    };
  }

  // Clicking the field in the window view: anchored under it; on phones the full-screen window.
  function onFieldFocus() {
    if (view !== 'window') {
      open = q.length > 0;
    } else if (!windowOpen) {
      if (phone()) {
        input.blur();
        anchored = false;
        windowOpen = true;
      } else openAnchored();
    }
  }
  function onFieldKey(e) {
    if (windowOpen && anchored) win?.handleKey(e);
    else onKey(e);
  }
</script>

<svelte:window onpageshow={onPageShow} />

<div class="search" class:wide={view === 'columns'} onfocusout={(e) => !e.currentTarget.contains(e.relatedTarget) && setTimeout(() => (open = false), 150)}>
  <svg class="glass" viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><circle cx="11" cy="11" r="7" /><path d="m20 20-3.5-3.5" /></svg>
  <input
    bind:this={input}
    bind:value={q}
    oninput={onInput}
    onfocus={onFieldFocus}
    onkeydown={onFieldKey}
    placeholder={(hasSearchServices ? t('Suchen in Diensten, Filmen, Serien, Dokumenten …') : t('Dienste suchen oder im Web suchen …')) + (shortcut ? `  ( ${shortcut} )` : '')}
    aria-label={t('Suche')}
    autocomplete="off"
  />
  {#if q && (view !== 'window' || anchored)}
    <button class="clear" onclick={() => reset({ focus: true })} aria-label={t('Suche leeren')} title={t('Suche leeren')}>
      <svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round"><path d="M6 6l12 12M18 6 6 18" /></svg>
    </button>
  {/if}

  {#if open && q.trim() && view === 'list'}
    <div class="panel card" tabindex="-1">
      {#if tileHits.length}
        <div class="section">{t('Kacheln')}</div>
        {#each tileHits as hit}
          <a class="hit" href={tileHref(hit, local)} target={newTab ? '_blank' : undefined} rel="noopener noreferrer" onclick={() => { remember(); setTimeout(reset); }}>
            <Icon icon={hit.icon} name={hit.name} favicon={faviconUrl(hit)} size={24} /> <span>{hit.name}</span>
          </a>
        {/each}
      {/if}

      {#each groups as g}
        {@const tile = tileById[g.tileId]}
        {#if g.items?.length || g.error}
          <div class="section"><Icon icon={g.icon} name={g.name} favicon={faviconUrl(tile)} size={16} /> {g.name}</div>
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

  {#if open && q.trim() && view === 'columns'}
    <!-- One column per service with hits; services without hits are named below. -->
    <div class="panel columns card" tabindex="-1">
      <div class="cols">
        {#if tileHits.length}
          <div class="col">
            <div class="col-head">{t('Kacheln')} <span class="count">{tileHits.length}</span></div>
            {#each tileHits as hit}
              <a class="hit" href={tileHref(hit, local)} target={newTab ? '_blank' : undefined} rel="noopener noreferrer" onclick={() => { remember(); setTimeout(reset); }}>
                <Icon icon={hit.icon} name={hit.name} favicon={faviconUrl(hit)} size={24} /> <span>{hit.name}</span>
              </a>
            {/each}
          </div>
        {/if}
        {#each groups.filter((g) => g.items?.length || g.error) as g (g.tileId)}
          {@const tile = tileById[g.tileId]}
          <div class="col">
            <div class="col-head"><Icon icon={g.icon} name={g.name} favicon={faviconUrl(tile)} size={18} /> {g.name} <span class="count">{g.items?.length || 0}</span></div>
            {#if g.error}
              <div class="err">{g.error}</div>
            {:else}
              {#each g.items as it}
                <div class="remote-item">
                  {#if it.image}<img src={rewrite(it.image, tile, local)} alt="" loading="lazy" onerror={(e) => (e.currentTarget.style.visibility = 'hidden')} />{/if}
                  <div class="grow"><Widget widget={{ items: [it] }} onAction={makeAction(g.tileId)} {newTab} compact={false} link={(u) => rewrite(u, tile, local)} /></div>
                </div>
              {/each}
            {/if}
          </div>
        {/each}
        {#if loading}
          <div class="col pending"><div class="col-head muted">{t('Suche in deinen Diensten …')}</div></div>
        {/if}
      </div>
      {#if !loading && noHits.length}
        <div class="none">{t('Keine Treffer in')}: {noHits.join(', ')}</div>
      {/if}
      <button class="hit web" onclick={webSearch}>🌐 {t('Im Web suchen: „{q}“', { q: q.trim() })}</button>
    </div>
  {/if}
</div>

{#if windowOpen}
  <SearchWindow bind:this={win} bind:q {anchored} {input} {parsed} {tileHits} {groups} {loading} {noHits} {tileById} {defs} {tiles} {local} {newTab} {recent} {hasSearchServices}
    {onInput} {makeAction} {webSearch} {openUrl} onForget={forget} onClose={() => reset()} />
{/if}

<style>
  .search { position: relative; width: 100%; max-width: 620px; }
  .glass { position: absolute; left: 14px; top: 50%; transform: translateY(-50%); color: var(--muted); pointer-events: none; }
  input { padding: 12px 44px 12px 40px; border-radius: 14px; background: var(--surface); backdrop-filter: blur(14px); border-color: var(--border); }
  /* Empties the field; only there while something is typed. */
  .clear {
    position: absolute; right: 8px; top: 50%; transform: translateY(-50%); width: 28px; height: 28px; padding: 0;
    display: grid; place-items: center; border-radius: 50%; border: 0; background: var(--surface-2); color: var(--muted);
  }
  .clear:hover { color: var(--text); background: var(--border-strong); }
  .panel {
    position: absolute; top: calc(100% + 6px); left: 0; right: 0; z-index: 40; background: var(--surface-solid);
    padding: 8px; max-height: min(70vh, 640px); overflow-y: auto; display: grid; gap: 2px;
  }
  /* Columns: wider than the field, centred under it. */
  .panel.columns { left: 50%; right: auto; width: min(1180px, calc(100vw - 32px)); transform: translateX(-50%); padding: 12px; gap: 8px; }
  .cols { display: grid; grid-template-columns: repeat(auto-fit, minmax(240px, 1fr)); gap: 10px; align-items: start; }
  .col { background: var(--surface-2); border: 1px solid var(--border); border-radius: var(--r-md); padding: 8px; display: grid; gap: 4px; min-width: 0; }
  .col-head { display: flex; gap: 6px; align-items: center; font-weight: 600; font-size: 13.5px; padding: 2px 4px 6px; border-bottom: 1px solid var(--border); margin-bottom: 2px; }
  .count { margin-left: auto; font-weight: 400; font-size: 12px; color: var(--muted); }
  .col.pending { border-style: dashed; }
  .section { display: flex; gap: 6px; align-items: center; font-size: 11.5px; text-transform: uppercase; letter-spacing: .06em; color: var(--muted); padding: 10px 8px 4px; }
  .hit { display: flex; gap: 10px; align-items: center; padding: 8px; border-radius: var(--r-sm); border: 0; background: none; text-align: left; width: 100%; }
  .hit:hover, .hit:focus { background: var(--surface-2); }
  .col .hit:hover, .col .hit:focus { background: var(--surface); }
  .web { color: var(--muted); margin-top: 4px; border-top: 1px solid var(--border); border-radius: 0 0 10px 10px; }
  .remote { padding: 0 8px; }
  .remote-item { display: flex; gap: 10px; align-items: center; }
  .remote-item img { width: 32px; height: 48px; object-fit: cover; border-radius: 4px; flex: none; }
  .grow { flex: 1; min-width: 0; }
  .none { font-size: 12.5px; color: var(--muted); padding: 8px 8px 2px; }
  .err { font-size: 12.5px; color: var(--bad); padding: 0 8px 6px; }
</style>
