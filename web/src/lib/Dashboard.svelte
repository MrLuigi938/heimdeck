<script>
  import { get, post, put } from './api.js';
  import { toast } from './toast.svelte.js';
  import Tile from './Tile.svelte';
  import Search from './Search.svelte';
  import TileEditor from './TileEditor.svelte';
  import Discover from './Discover.svelte';
  import Settings from './Settings.svelte';
  import GroupEditor from './GroupEditor.svelte';
  import Uptime from './Uptime.svelte';
  import { network, isLocal, setOverride, detectLocal } from './network.svelte.js';
  import { t, locale } from './i18n.svelte.js';
  import { promptDialog } from './dialog.svelte.js';

  let data = $state(null);
  let status = $state({});
  let editMode = $state(false);
  let editing = $state(null); // tile object (or {groupId} for a new one)
  let showDiscover = $state(false);
  let showSettings = $state(false);
  let now = $state(new Date());
  let dragging = $state(null);
  let dropTarget = $state(null);
  let editingGroup = $state(null);
  let unlockPw = $state({});
  let showHidden = $state(readHidden());
  let boardMenu = $state(false);

  // The open dashboard: ?board=<id> in the address wins (for bookmarks),
  // otherwise the one last used in this browser.
  let boardId = $state(readBoard());
  function readBoard() {
    const fromUrl = Number(new URLSearchParams(location.search).get('board'));
    if (fromUrl) return fromUrl;
    try {
      return Number(localStorage.getItem('heimdeck-board')) || 0;
    } catch {
      return 0;
    }
  }
  function switchBoard(id) {
    boardId = id;
    boardMenu = false;
    try {
      localStorage.setItem('heimdeck-board', String(id));
    } catch {
      /* private mode */
    }
    const url = new URL(location.href);
    url.searchParams.delete('board');
    history.replaceState(null, '', url);
  }

  function readHidden() {
    try {
      return sessionStorage.getItem('heimdeck-show-hidden') === '1';
    } catch {
      return false;
    }
  }
  function toggleHidden() {
    showHidden = !showHidden;
    try {
      sessionStorage.setItem('heimdeck-show-hidden', showHidden ? '1' : '0');
    } catch {
      /* private mode */
    }
  }

  let settings = $derived(data?.settings || {});
  let defs = $derived(Object.fromEntries((data?.integrations || []).map((d) => [d.id, d])));
  let boards = $derived(data?.boards || []);
  let board = $derived(boards.find((b) => b.id === boardId) || boards[0]);
  let boardGroups = $derived((data?.groups || []).filter((g) => g.boardId === board?.id));
  let allTiles = $derived(boardGroups.flatMap((g) => g.tiles));
  let newTab = $derived(settings.openInNewTab !== 'false');
  let hasSearchServices = $derived(allTiles.some((t) => defs[t.integration]?.hasSearch));
  // Groups a tile can be moved to; with several dashboards labelled "Dashboard › Group", current one first.
  let groupOptions = $derived.by(() => {
    const open = (data?.groups || []).filter((g) => !g.locked);
    const name = (g) => t(boards.find((b) => b.id === g.boardId)?.name || '');
    const sorted = [...open.filter((g) => g.boardId === board?.id), ...open.filter((g) => g.boardId !== board?.id)];
    return boards.length > 1 ? sorted.map((g) => ({ ...g, name: `${name(g)} › ${g.name}` })) : sorted;
  });
  let hasHidden = $derived(boardGroups.some((g) => g.visibility === 'hidden'));
  let hiddenCount = $derived(boardGroups.filter((g) => g.visibility === 'hidden').length);
  // A group can override the global "uniform tiles" setting.
  const isUniform = (g) => g.layout === 'uniform' || (g.layout !== 'auto' && settings.uniformTiles === 'true');
  let hasExternal = $derived(allTiles.some((t) => t.externalUrl));
  $effect(() => {
    network.localHosts = settings.localHosts || '';
  });
  let local = $derived(isLocal());
  let autoLocal = $derived(detectLocal(network.localHosts));

  // Header switch: follow auto-detection, or force "home"/"away" for this session.
  function toggleNetwork() {
    const next = local ? 'remote' : 'local';
    setOverride((next === 'local') === autoLocal ? '' : next);
  }

  async function unlock(e, g) {
    e.preventDefault();
    try {
      await post(`/groups/${g.id}/unlock`, { password: unlockPw[g.id] || '' });
      unlockPw[g.id] = '';
      await load();
      loadStatus();
    } catch (err) {
      toast(err.message, 'bad');
    }
  }
  async function lock(g) {
    await post(`/groups/${g.id}/lock`);
    load();
  }

  async function load() {
    try {
      data = await get('/dashboard');
    } catch (e) {
      toast(e.message, 'bad');
    }
  }

  async function loadStatus() {
    try {
      status = await get('/status');
    } catch {
      /* ignore, retried */
    }
  }

  load().then(loadStatus);
  $effect(() => {
    const s = setInterval(() => document.visibilityState === 'visible' && loadStatus(), 30000);
    const c = setInterval(() => (now = new Date()), 15000);
    return () => { clearInterval(s); clearInterval(c); };
  });

  // Theme, accent colour and background follow the settings.
  $effect(() => {
    const root = document.documentElement;
    const media = matchMedia('(prefers-color-scheme: light)');
    const apply = () => {
      const theme = settings.theme === 'light' || (settings.theme !== 'dark' && media.matches) ? 'light' : 'dark';
      root.dataset.theme = theme;
    };
    apply();
    media.addEventListener('change', apply);
    root.style.setProperty('--accent', settings.accent || '#5b8cff');
    document.title = settings.title || 'Heimdeck';
    if (settings.background) {
      document.body.style.setProperty('--bg-image', `url("${settings.background.replace(/"/g, '%22')}")`);
      document.body.classList.add('has-bg');
    } else {
      document.body.style.removeProperty('--bg-image');
      document.body.classList.remove('has-bg');
    }
    return () => media.removeEventListener('change', apply);
  });

  let greeting = $derived.by(() => {
    const h = now.getHours();
    const who = data?.username ? `, ${data.username}` : '';
    if (h < 5) return t('Gute Nacht') + who;
    if (h < 11) return t('Guten Morgen') + who;
    if (h < 18) return t('Hallo') + who;
    return t('Guten Abend') + who;
  });
  let dateText = $derived(now.toLocaleDateString(locale(), { weekday: 'long', day: 'numeric', month: 'long' }));
  let timeText = $derived(now.toLocaleTimeString(locale(), { hour: '2-digit', minute: '2-digit' }));

  // ---- groups ----
  async function addGroup() {
    const name = await promptDialog(t('Name der neuen Gruppe'), { title: t('Neue Gruppe'), confirmLabel: t('Anlegen') });
    if (!name) return;
    await post('/groups', { name, boardId: board?.id });
    load();
  }
  function moveGroup(i, dir) {
    const a = boardGroups[i];
    const b = boardGroups[i + dir];
    if (!a || !b) return;
    const gs = data.groups;
    const ia = gs.indexOf(a);
    const ib = gs.indexOf(b);
    [gs[ia], gs[ib]] = [gs[ib], gs[ia]];
    saveLayout();
  }

  // ---- drag & drop ----
  async function saveLayout() {
    try {
      await put('/layout', data.groups.map((g) => ({ groupId: g.id, tiles: g.tiles.map((t) => t.id) })));
    } catch (e) {
      toast(e.message, 'bad');
      load();
    }
  }

  function moveTile(tileId, groupId, beforeId) {
    let tile;
    for (const g of data.groups) {
      const i = g.tiles.findIndex((t) => t.id === tileId);
      if (i >= 0) [tile] = g.tiles.splice(i, 1);
    }
    if (!tile) return;
    const target = data.groups.find((g) => g.id === groupId);
    tile.groupId = groupId;
    const idx = beforeId == null ? -1 : target.tiles.findIndex((t) => t.id === beforeId);
    if (idx < 0) target.tiles.push(tile);
    else target.tiles.splice(idx, 0, tile);
    saveLayout();
  }

  function dragPropsFor(tile, group) {
    if (!editMode) return {};
    return {
      draggable: 'true',
      ondragstart: (e) => {
        dragging = tile.id;
        e.dataTransfer.effectAllowed = 'move';
        e.dataTransfer.setData('text/plain', String(tile.id));
      },
      ondragend: () => { dragging = null; dropTarget = null; },
      ondragover: (e) => { e.preventDefault(); e.stopPropagation(); dropTarget = tile.id; },
      ondrop: (e) => {
        e.preventDefault();
        e.stopPropagation();
        if (dragging != null && dragging !== tile.id) moveTile(dragging, group.id, tile.id);
        dragging = null;
        dropTarget = null;
      },
    };
  }

  function groupDrop(e, group) {
    e.preventDefault();
    if (dragging != null) moveTile(dragging, group.id, null);
    dragging = null;
    dropTarget = null;
  }

  function saved(opts) {
    if (!opts?.keepOpen) {
      editing = null;
      editingGroup = null;
      showDiscover = false;
      showSettings = false;
    }
    load().then(loadStatus);
    // The server re-checks a saved tile right away; pick the result up shortly after.
    setTimeout(loadStatus, 4000);
  }
</script>

<svelte:window onclick={(e) => boardMenu && !e.target.closest('.boards') && (boardMenu = false)} />

{#if data}
  <div class="page">
    <header class="top">
      <div class="brand on-bg">
        <div class="title-row">
          <h1>{settings.title || 'Heimdeck'}</h1>
          {#if boards.length > 1}
            <div class="boards">
              <button class="board-btn" onclick={() => (boardMenu = !boardMenu)} aria-expanded={boardMenu} title={t('Dashboard wechseln')}>
                <span>{board?.icon || '▦'}</span><span class="board-name">{t(board?.name || '')}</span><span class="caret">▾</span>
              </button>
              {#if boardMenu}
                <div class="board-menu card" role="menu">
                  {#each boards as b (b.id)}
                    <button role="menuitem" class:on={b.id === board?.id} onclick={() => switchBoard(b.id)}>{b.icon || '▦'} {t(b.name)}</button>
                  {/each}
                </div>
              {/if}
            </div>
          {/if}
        </div>
        {#if settings.showGreeting !== 'false'}
          <div class="muted greet">{greeting}<span class="greet-date">{' · ' + dateText}</span> · {timeText}</div>
        {/if}
      </div>
      <Search tiles={allTiles} {newTab} searchEngine={settings.searchEngine} {hasSearchServices} {local} boardId={board?.id} />
      <div class="tools on-bg">
        {#if hasExternal}
          <button class="ghost net" onclick={toggleNetwork}
            title={`${local ? t('Zuhause: lokale Adressen werden geöffnet') : t('Unterwegs: öffentliche Adressen werden geöffnet')} (${network.override ? t('manuell gewählt') : t('automatisch erkannt')}) – ${t('klicken zum Umschalten')}`}>
            {local ? '🏠' : '🌐'}<span class="net-label">{local ? t('Zuhause') : t('Unterwegs')}</span>{#if network.override}<span class="manual">•</span>{/if}
          </button>
        {/if}
        {#if hasHidden}
          <button class="ghost icon" class:on={showHidden} title={showHidden ? t('Ausgeblendete Gruppen verbergen') : t('Ausgeblendete Gruppen anzeigen')} aria-label={t('Ausgeblendete Gruppen anzeigen')} onclick={toggleHidden}>
            <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12z" /><circle cx="12" cy="12" r="3" /></svg>
          </button>
        {/if}
        {#if data.docker}
          <button class="ghost icon" title={t('Container automatisch finden')} aria-label={t('Container automatisch finden')} onclick={() => (showDiscover = true)}>🐳</button>
        {/if}
        <button class="ghost icon" class:on={editMode} title={t('Anordnen & bearbeiten')} aria-label={t('Anordnen & bearbeiten')} onclick={() => (editMode = !editMode)}>
          <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 20h9" /><path d="M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4z" /></svg>
        </button>
        <button class="ghost icon" title={t('Einstellungen')} aria-label={t('Einstellungen')} onclick={() => (showSettings = true)}>
          <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="3" /><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 1 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 1 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 1 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 1 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z" /></svg>
        </button>
      </div>
    </header>

    {#if editMode}
      <div class="editbar card">
        <span>✎ {t('Bearbeitungsmodus: Kacheln per Drag & Drop verschieben, anklicken zum Bearbeiten.')}</span>
        <span class="spacer"></span>
        <button class="small" onclick={addGroup}>+ {t('Gruppe')}</button>
        <button class="small primary" onclick={() => (editMode = false)}>{t('Fertig')}</button>
      </div>
    {/if}

    {#if allTiles.length === 0}
      <div class="empty card">
        <h2>{t("Los geht's")} 👋</h2>
        <p class="muted">{t('Dein Dashboard ist noch leer. Füge deine Dienste hinzu – am schnellsten automatisch über Docker.')}</p>
        <div class="row center">
          {#if data.docker}<button class="primary" onclick={() => (showDiscover = true)}>🐳 {t('Container automatisch finden')}</button>{/if}
          <button class:primary={!data.docker} onclick={() => (editing = { groupId: boardGroups.find((g) => !g.locked)?.id })} disabled={!boardGroups.some((g) => !g.locked)}>+ {t('Kachel hinzufügen')}</button>
        </div>
        {#if !data.docker}
          <p class="help">{t('Tipp: Wenn du den Docker-Socket einbindest, erkennt Heimdeck deine Container automatisch (siehe docker-compose.yml).')}</p>
        {/if}
        {#if !boardGroups.length}<button onclick={addGroup}>+ {t('Gruppe anlegen')}</button>{/if}
      </div>
    {/if}

    {#each boardGroups as group, gi (group.id)}
      {#if group.locked}
        <form class="locked card" onsubmit={(e) => unlock(e, group)}>
          <span class="lock-name">🔒 {group.name}</span>
          <span class="spacer"></span>
          <input type="password" placeholder={t('Passwort')} bind:value={unlockPw[group.id]} autocomplete="off" aria-label={t('Passwort für {name}', { name: group.name })} />
          <button class="small">{t('Entsperren')}</button>
        </form>
      {:else if group.visibility !== 'hidden' || showHidden || editMode}
        <section class="group" aria-label={group.name} ondragover={(e) => editMode && e.preventDefault()} ondrop={(e) => editMode && groupDrop(e, group)}>
          <div class="group-head">
            <h2 class="on-bg">{group.name}</h2>
            {#if group.visibility === 'hidden'}<span class="vis on-bg" title={t('Ausgeblendete Gruppe')}>👁 {t('ausgeblendet')}</span>{/if}
            {#if group.visibility === 'protected'}
              <button class="ghost small vis on-bg" title={t('Gruppe wieder sperren')} onclick={() => lock(group)}>🔓 {t('sperren')}</button>
            {/if}
            {#if editMode}
              <div class="group-tools on-bg">
                <button class="ghost small" onclick={() => moveGroup(gi, -1)} disabled={gi === 0} title={t('Nach oben')}>↑</button>
                <button class="ghost small" onclick={() => moveGroup(gi, 1)} disabled={gi === boardGroups.length - 1} title={t('Nach unten')}>↓</button>
                <button class="ghost small" onclick={() => (editingGroup = group)}>{t('Bearbeiten')}</button>
              </div>
            {/if}
          </div>
          <div class="grid" class:uniform={isUniform(group)}>
            {#if !group.tiles.length && !editMode}
              <p class="empty-group on-bg">{t('Noch keine Kacheln – über ✎ oben rechts hinzufügen.')}</p>
            {/if}
            {#each group.tiles as tile (tile.id)}
              <div class="slot" class:drop={dropTarget === tile.id && dragging !== tile.id} class:dragging={dragging === tile.id}>
                <Tile {tile} def={defs[tile.integration]} status={status[tile.id]} {editMode} {newTab} {local} uniform={isUniform(group)}
                  onEdit={() => (editing = tile)} dragProps={dragPropsFor(tile, group)} />
              </div>
            {/each}
            {#if editMode}
              <button class="add" onclick={() => (editing = { groupId: group.id })}>+ {t('Kachel')}</button>
            {/if}
          </div>
        </section>
      {/if}
    {/each}

    {#if hiddenCount && !showHidden && !editMode}
      <button class="hidden-hint on-bg" onclick={toggleHidden}>
        👁 {hiddenCount === 1 ? t('1 ausgeblendete Gruppe – anzeigen') : t('{n} ausgeblendete Gruppen – anzeigen', { n: hiddenCount })}
      </button>
    {/if}

    {#if allTiles.length && settings.showUptime !== 'false'}
      <Uptime tiles={allTiles} {status} />
    {/if}
  </div>

  {#if editing}
    <TileEditor tile={editing} groups={groupOptions} integrations={data.integrations} onClose={() => (editing = null)} onSaved={saved} />
  {/if}
  {#if showDiscover}
    <Discover groups={boardGroups.filter((g) => !g.locked)} boardId={board?.id} onClose={() => (showDiscover = false)} onSaved={saved} />
  {/if}
  {#if editingGroup}
    <GroupEditor group={editingGroup} {boards} onClose={() => (editingGroup = null)} onSaved={saved} />
  {/if}
  {#if showSettings}
    <Settings {settings} {boards} username={data.username} onSwitchBoard={switchBoard} onClose={() => (showSettings = false)} onSaved={saved} />
  {/if}
{/if}

<style>
  .page { max-width: 1400px; margin: 0 auto; padding: 28px 24px 80px; display: grid; gap: 28px; }
  /* z-index lifts the header (and its dropdowns) above the cards below, which form their own layers. */
  .top { display: grid; grid-template-columns: 1fr minmax(0, 620px) 1fr; gap: 16px; align-items: center; position: relative; z-index: 30; }
  /* The brand block (with the dashboard menu) sits above the icons and search next to it. */
  .brand { min-width: 0; position: relative; z-index: 2; }
  h1 { margin: 0; font-size: 24px; letter-spacing: -.01em; }
  .greet { font-size: 13.5px; margin-top: 2px; }
  .title-row { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; }
  .boards { position: relative; }
  .board-btn { padding: 4px 10px; font-size: 13px; border-radius: 999px; display: inline-flex; gap: 6px; align-items: center; }
  .caret { font-size: 10px; color: var(--muted); }
  .board-menu { position: absolute; top: calc(100% + 6px); left: 0; z-index: 45; min-width: 180px; padding: 6px; display: grid; gap: 2px; background: var(--surface-solid); }
  .board-menu button { border: 0; background: none; text-align: left; padding: 8px 10px; border-radius: 8px; }
  .board-menu button:hover, .board-menu button.on { background: var(--surface-2); }
  .empty-group { grid-column: 1 / -1; margin: 0; font-size: 13px; color: var(--muted); padding: 10px 14px; border: 1px dashed var(--border-strong); border-radius: var(--radius); }
  .tools { display: flex; justify-content: flex-end; gap: 4px; }
  .tools .on { background: var(--accent); color: #fff; }
  .hidden-hint { justify-self: center; background: transparent; border: 1px dashed var(--border-strong); color: var(--muted); font-size: 13px; padding: 6px 14px; border-radius: 999px; }

  .editbar { display: flex; gap: 10px; align-items: center; padding: 10px 14px; font-size: 14px; flex-wrap: wrap; }
  .group { display: grid; gap: 12px; }
  .group-head { display: flex; align-items: center; gap: 8px; min-height: 30px; }
  .group-head h2 { margin: 0; font-size: 13px; text-transform: uppercase; letter-spacing: .08em; color: var(--muted); font-weight: 600; }
  .group-tools { display: flex; gap: 2px; flex-wrap: wrap; }
  .vis { font-size: 12px; color: var(--muted); }
  .locked { display: flex; align-items: center; gap: 10px; padding: 10px 14px; flex-wrap: wrap; }
  .lock-name { font-size: 13px; text-transform: uppercase; letter-spacing: .08em; color: var(--muted); font-weight: 600; }
  .locked input { width: 180px; padding: 6px 10px; }
  .net { display: inline-flex; align-items: center; gap: 6px; padding: 6px 10px; font-size: 13px; color: var(--muted); }
  .manual { color: var(--accent); }
  .grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(210px, 1fr)); gap: 12px; grid-auto-flow: dense; align-items: start; }
  /* Uniform: every tile one column of the same width; tiles in a row share the tallest one's height. */
  .grid.uniform { grid-template-columns: repeat(auto-fill, minmax(300px, 1fr)); align-items: stretch; }
  .slot { display: contents; }
  .slot.drop > :global(.tile) { border-color: var(--accent); transform: translateZ(0) scale(1.02); }
  .slot.dragging > :global(.tile) { opacity: .4; }
  .add { border: 1px dashed var(--border-strong); background: transparent; min-height: 68px; border-radius: var(--radius); color: var(--muted); }
  .empty { padding: 36px 24px; text-align: center; display: grid; gap: 12px; justify-items: center; }
  .empty h2 { margin: 0; }
  .empty p { margin: 0; max-width: 520px; }
  .center { justify-content: center; flex-wrap: wrap; }

  @media (max-width: 900px) {
    .top { grid-template-columns: 1fr auto; }
    .top :global(.search) { grid-column: 1 / -1; grid-row: 2; max-width: none; }
  }
  @media (max-width: 640px) {
    .net-label { display: none; }
    /* Icons in a compact block (3 + 2) that matches the height of the title block. */
    .tools { display: grid; grid-template-columns: repeat(3, 38px); justify-content: center; align-content: center; align-self: stretch; gap: 2px; }
    .tools :global(button) { width: 38px; height: 38px; padding: 0; justify-content: center; }
    .locked { display: grid; grid-template-columns: minmax(0, 1fr); }
    .locked .spacer { display: none; }
    .locked input { width: 100%; }
    .page { padding: 16px 16px 60px; gap: 22px; }
    .grid { grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 10px; }
    .grid.uniform { grid-template-columns: minmax(0, 1fr); }
  }
</style>
