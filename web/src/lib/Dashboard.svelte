<script>
  import { get, post, put, del, setDebug } from './api.js';
  import { toast } from './toast.svelte.js';
  import Tile from './Tile.svelte';
  import FreeGrid, { byPosition } from './FreeGrid.svelte';
  import Search from './Search.svelte';
  import TileEditor from './TileEditor.svelte';
  import Discover from './Discover.svelte';
  import { importFile } from './importFile.js';
  import { browserNotify, checkAlerts } from './browserNotify.svelte.js';
  import Settings from './Settings.svelte';
  import GroupEditor from './GroupEditor.svelte';
  import Uptime from './Uptime.svelte';
  import Credit from './Credit.svelte';
  import { network, isLocal, setOverride, detectLocal } from './network.svelte.js';
  import { t, locale } from './i18n.svelte.js';
  import { promptDialog } from './dialog.svelte.js';
  import Modal from './Modal.svelte';
  import { ACTIONS, keymap, blocked, isBoardKey, keyLabel } from './shortcuts.js';
  import { applyTheme, activeSeason } from './themes.js';
  import SeasonFx from './SeasonFx.svelte';

  let data = $state(null);
  let status = $state({});
  let editMode = $state(false);
  let editing = $state(null); // tile object (or {groupId} for a new one)
  let showDiscover = $state(false);
  let showSettings = $state(false);
  let settingsSection = $state(''); // the area the settings open on, e.g. "notify"
  function openSettings(section = '') {
    settingsSection = section;
    showSettings = true;
  }
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
  // A dashboard someone else shared: only to look at, buttons only if allowed.
  let readOnly = $derived(!!board?.shared);
  let admin = $derived(data?.role === 'admin');
  $effect(() => {
    if (readOnly) editMode = false;
  });
  let ownBoards = $derived(boards.filter((b) => !b.shared));
  let sharedBoards = $derived(boards.filter((b) => b.shared));
  let boardGroups = $derived((data?.groups || []).filter((g) => g.boardId === board?.id));
  let allTiles = $derived(boardGroups.flatMap((g) => g.tiles));
  let newTab = $derived(settings.openInNewTab !== 'false');
  let hasSearchServices = $derived(allTiles.some((t) => defs[t.integration]?.hasSearch));
  // Groups a tile can be moved to; with several dashboards labelled "Dashboard › Group", current one first.
  // Only groups on the user's own dashboards.
  let groupOptions = $derived.by(() => {
    const own = new Set(ownBoards.map((b) => b.id));
    const open = (data?.groups || []).filter((g) => !g.locked && own.has(g.boardId));
    const name = (g) => t(boards.find((b) => b.id === g.boardId)?.name || '');
    const sorted = [...open.filter((g) => g.boardId === board?.id), ...open.filter((g) => g.boardId !== board?.id)];
    return ownBoards.length > 1 ? sorted.map((g) => ({ ...g, name: `${name(g)} › ${g.name}` })) : sorted;
  });

  // Dashboards offered by others, waiting for an answer.
  async function answerShare(s, yes) {
    try {
      if (yes) await post(`/boards/${s.boardId}/accept`);
      else await del(`/boards/${s.boardId}/shares/${s.userId}`);
      await load();
      if (yes) {
        toast(t('„{board}“ ist jetzt in deiner Dashboard-Liste.', { board: t(s.boardName) }));
        switchBoard(s.boardId);
      }
    } catch (e) {
      toast(e.message, 'bad');
    }
  }
  // The groups to jump to from the search: the ones shown, hidden ones only while they are shown.
  let jumpTargets = $derived(
    boardGroups.filter((g) => g.visibility !== 'hidden' || showHidden).map((g) => ({ id: g.id, name: g.name, count: g.tiles.length })),
  );
  // Jump bar under the search (setting jumpBar, from three groups on) and, on
  // phones, a round button that opens the groups as a sheet (setting jumpButton).
  let showJumpBar = $derived(settings.jumpBar !== 'false' && jumpTargets.length >= 3);
  let showJumpButton = $derived(settings.jumpButton !== 'false' && jumpTargets.length >= 2);
  let activeGroup = $state(0);
  let scrolledDown = $state(false);
  let jumpSheet = $state(false);
  let jumpBarEl = $state();
  // The group being read: the last one whose title is above a line just below the bar.
  let barStuck = $state(false);
  let jumpLock = 0;
  function trackGroup() {
    scrolledDown = scrollY > 300;
    // Stuck to the top: the bar gets a background so the tiles below do not shine through.
    barStuck = !!jumpBarEl && scrollY > 0 && jumpBarEl.getBoundingClientRect().top <= 1;
    // Right after a jump the chosen group stays marked, even if the page cannot scroll that far.
    if (Date.now() < jumpLock) return;
    // Groups jumped to land 70px from the top (scroll-margin-top), safely above this edge.
    const edge = Math.max(jumpBarEl?.getBoundingClientRect().bottom || 0, 0) + 56;
    let current = 0;
    for (const g of jumpTargets) {
      const el = document.getElementById(`group-${g.id}`);
      if (el && el.getBoundingClientRect().top <= edge) current = g.id;
    }
    // At the very bottom the last groups can never reach the top: then the last one counts.
    if (innerHeight + scrollY >= document.documentElement.scrollHeight - 4 && jumpTargets.length) current = jumpTargets[jumpTargets.length - 1].id;
    activeGroup = current;
  }
  let trackFrame = 0;
  const onPageScroll = () => {
    cancelAnimationFrame(trackFrame);
    trackFrame = requestAnimationFrame(trackGroup);
  };
  $effect(() => {
    jumpTargets;
    trackGroup();
  });
  // The active chip stays in view when the bar scrolls sideways.
  $effect(() => {
    if (!activeGroup || !jumpBarEl) return;
    const chip = jumpBarEl.querySelector(`[data-group="${activeGroup}"]`);
    if (chip) {
      const bar = jumpBarEl.querySelector('.jump-chips');
      const left = chip.offsetLeft - bar.clientWidth / 2 + chip.clientWidth / 2;
      bar.scrollTo({ left, behavior: 'smooth' });
    }
  });
  let flashGroup = $state(0);
  function jumpToGroup(id) {
    jumpSheet = false;
    activeGroup = id;
    jumpLock = Date.now() + 1200;
    const el = document.getElementById(`group-${id}`);
    if (!el) return;
    el.scrollIntoView({ behavior: matchMedia('(prefers-reduced-motion: reduce)').matches ? 'instant' : 'smooth', block: 'start' });
    flashGroup = id;
    setTimeout(() => flashGroup === id && (flashGroup = 0), 1600);
  }
  let hasHidden = $derived(boardGroups.some((g) => g.visibility === 'hidden'));
  let hiddenCount = $derived(boardGroups.filter((g) => g.visibility === 'hidden').length);
  // A group can override the global tile size (setting tileLayout; older
  // installs only have the uniformTiles switch).
  // A free grid sizes each tile itself; on phones its tiles show like "auto".
  const layoutOf = (g) => (g.arrange === 'free' ? 'auto' : g.layout || settings.tileLayout || (settings.uniformTiles === 'true' ? 'uniform' : 'auto'));
  const isUniform = (g) => layoutOf(g) === 'uniform';
  const compactOf = (g) => (layoutOf(g).startsWith('icons') ? layoutOf(g) : '');
  const isFree = (g) => g.arrange === 'free';
  // On phones a freely arranged group is shown scaled down to 4 columns.
  let narrow = $state(matchMedia('(max-width: 640px)').matches);
  $effect(() => {
    const media = matchMedia('(max-width: 640px)');
    const update = () => (narrow = media.matches);
    media.addEventListener('change', update);
    return () => media.removeEventListener('change', update);
  });
  let hasExternal = $derived(allTiles.some((t) => t.externalUrl));
  $effect(() => {
    network.localHosts = settings.localHosts || '';
  });
  $effect(() => setDebug(!!data?.debug));
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
      checkAlerts(status, (data?.groups || []).flatMap((g) => g.tiles || []));
    } catch {
      /* ignore, retried */
    }
  }

  load().then(loadStatus);
  $effect(() => {
    // With browser notifications on, keep polling in background tabs too.
    const s = setInterval(() => (document.visibilityState === 'visible' || browserNotify.on) && loadStatus(), 30000);
    const c = setInterval(() => (now = new Date()), 15000);
    return () => { clearInterval(s); clearInterval(c); };
  });

  // Theme, accent colour and background follow the settings; a dashboard can have its own theme.
  $effect(() => {
    const media = matchMedia('(prefers-color-scheme: light)');
    const apply = () => applyTheme(settings, board?.theme);
    apply();
    media.addEventListener('change', apply);
    document.title = settings.title || 'Heimdeck';
    // A dashboard can have its own background; otherwise the global one is used.
    const background = board?.background || settings.background;
    if (background) {
      document.body.style.setProperty('--bg-image', `url("${background.replace(/"/g, '%22')}")`);
      document.body.classList.add('has-bg');
    } else {
      document.body.style.removeProperty('--bg-image');
      document.body.classList.remove('has-bg');
    }
    return () => media.removeEventListener('change', apply);
  });

  // The festive season in effect; its moving effects can be turned off (setting seasonalFx).
  let season = $derived(settings.seasonalFx === 'false' ? '' : activeSeason(settings, board?.theme)?.id || '');

  // A newer release that is out for more than three days: a red dot on the
  // settings button, and once per browser session a hint for five seconds.
  const STALE_DAYS = 3;
  // ?updatehint in the address shows dot and hint right away, to try them out
  // (only when a newer version is actually there).
  const forceHint = new URLSearchParams(location.search).has('updatehint');
  let updateStale = $derived(!!data?.update?.published && (forceHint || Date.now() - Date.parse(data.update.published) > STALE_DAYS * 86400e3));
  let updateBubble = $state(false);
  $effect(() => {
    if (!updateStale) return;
    const key = 'heimdeck-update-hint';
    try {
      if (!forceHint && sessionStorage.getItem(key) === data.update.version) return;
      sessionStorage.setItem(key, data.update.version);
    } catch {
      /* private mode: show it anyway */
    }
    updateBubble = true;
    const timer = setTimeout(() => (updateBubble = false), 5000);
    return () => clearTimeout(timer);
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

  // ---- keyboard shortcuts ----
  let searchBox;
  let showKeys = $state(false);
  let keys = $derived(keymap(settings));
  let shortcutsOn = $derived(settings.shortcutsOff !== 'true');
  function onShortcut(e) {
    if (!data || !shortcutsOn || blocked(e)) return;
    if (isBoardKey(e.key)) {
      const b = boards[Number(e.key) - 1];
      if (b) {
        e.preventDefault();
        switchBoard(b.id);
      }
      return;
    }
    const action = Object.keys(keys).find((a) => keys[a] && keys[a] === e.key);
    if (!action) return;
    e.preventDefault();
    const step = (dir) => {
      const i = boards.findIndex((b) => b.id === board?.id);
      if (boards.length > 1) switchBoard(boards[(i + dir + boards.length) % boards.length].id);
    };
    ({
      search: () => searchBox?.focus(),
      edit: () => !readOnly && (editMode = !editMode),
      settings: () => openSettings(),
      hidden: () => hasHidden && toggleHidden(),
      network: () => hasExternal && toggleNetwork(),
      prevBoard: () => step(-1),
      nextBoard: () => step(1),
      help: () => (showKeys = true),
    })[action]?.();
  }

  // Log out with two clicks: the first one asks, so a slip of the finger does not log out.
  let logoutArmed = $state(false);
  let logoutTimer;
  async function logoutClick() {
    if (!logoutArmed) {
      logoutArmed = true;
      clearTimeout(logoutTimer);
      logoutTimer = setTimeout(() => (logoutArmed = false), 4000);
      return;
    }
    clearTimeout(logoutTimer);
    try {
      await post('/logout');
    } finally {
      location.reload();
    }
  }

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
      await put('/layout', data.groups.map((g) => ({
        groupId: g.id,
        tiles: g.tiles.map((t) => t.id),
        grid: isFree(g) ? Object.fromEntries(g.tiles.filter((t) => t.grid).map((t) => [t.id, t.grid])) : undefined,
      })));
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
    if (tile.groupId !== groupId) tile.grid = null; // the server drops it as well
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
        grab = { x: 0, y: 0 }; // dropped into a free grid, the tile lands with its top left at the pointer
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

  // In a free grid the tile only starts the drag; the grid finds the cell.
  let grab = { x: 0, y: 0 };
  function freeDragProps(tile) {
    if (!editMode) return {};
    return {
      draggable: 'true',
      ondragstart: (e) => {
        dragging = tile.id;
        const r = e.currentTarget.getBoundingClientRect();
        grab = { x: e.clientX - r.left, y: e.clientY - r.top };
        e.dataTransfer.effectAllowed = 'move';
        e.dataTransfer.setData('text/plain', String(tile.id));
      },
      ondragend: () => { dragging = null; dropTarget = null; },
    };
  }
  // A free grid reports the new positions; a tile dropped in from another group moves along.
  function commitGrid(group, grid, incomingId) {
    if (incomingId != null && !group.tiles.some((t) => t.id === incomingId)) {
      for (const g of data.groups) {
        const i = g.tiles.findIndex((t) => t.id === incomingId);
        if (i >= 0) {
          const [tile] = g.tiles.splice(i, 1);
          tile.groupId = group.id;
          group.tiles.push(tile);
        }
      }
    }
    for (const t of group.tiles) if (grid[t.id]) t.grid = grid[t.id];
    group.tiles = byPosition(group.tiles);
    saveLayout();
  }
  const findTile = (id) => data.groups.flatMap((g) => g.tiles).find((t) => t.id === id);

  function groupDrop(e, group) {
    e.preventDefault();
    if (dragging != null) moveTile(dragging, group.id, null);
    dragging = null;
    dropTarget = null;
  }

  // Adds a Proxmox tile (address prefilled when Heimdeck knows it) and opens
  // the discovery right after it has been saved.
  let discoverAfterSave = false;
  async function connectProxmox() {
    const open = boardGroups.filter((g) => !g.locked);
    let gid = (open.find((g) => ['system', 'server'].includes(g.name.toLowerCase())) || open[0])?.id;
    if (!gid) {
      gid = (await post('/groups', { name: 'System', boardId: board?.id })).id;
      await load();
    }
    discoverAfterSave = true;
    editing = { groupId: gid, integration: 'proxmox', name: 'Proxmox', icon: 'proxmox', url: data.proxmoxHint || '', insecure: true };
  }

  function saved(opts) {
    if (!opts?.keepOpen) {
      editing = null;
      editingGroup = null;
      showDiscover = false;
      showSettings = false;
    }
    const discover = discoverAfterSave && !opts?.keepOpen;
    discoverAfterSave = false;
    load().then(() => {
      loadStatus();
      if (discover && data.proxmox) showDiscover = true;
    });
    // The server re-checks a saved tile right away; pick the result up shortly after.
    setTimeout(loadStatus, 4000);
  }
</script>

<svelte:window onclick={(e) => boardMenu && !e.target.closest('.boards') && (boardMenu = false)} onkeydown={onShortcut} onscroll={onPageScroll} onresize={onPageScroll} />

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
                  {#each ownBoards as b (b.id)}
                    <button role="menuitem" class:on={b.id === board?.id} onclick={() => switchBoard(b.id)}>{b.icon || '▦'} {t(b.name)}</button>
                  {/each}
                  {#if sharedBoards.length}
                    <div class="menu-label">{t('Mit dir geteilt')}</div>
                    {#each sharedBoards as b (b.id)}
                      <button role="menuitem" class:on={b.id === board?.id} onclick={() => switchBoard(b.id)}>{b.icon || '▦'} {t(b.name)} <span class="menu-owner">· {b.owner}</span></button>
                    {/each}
                  {/if}
                </div>
              {/if}
            </div>
          {/if}
        </div>
        {#if settings.showGreeting !== 'false'}
          <div class="muted greet">
            <div>{greeting}</div>
            <div class="when">{dateText} · {timeText}</div>
          </div>
        {/if}
      </div>
      <Search bind:this={searchBox} tiles={allTiles} sections={jumpTargets} onJump={jumpToGroup} {defs} view={settings.searchView || 'window'} jumpTop={settings.searchJump === 'true'} history={settings.searchHistory !== 'false'} {newTab} searchEngine={settings.searchEngine} searchUrl={settings.searchUrl} {hasSearchServices} {local} boardId={board?.id}
        shortcut={shortcutsOn ? keys.search : ''} />
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
        {#if (data.docker || data.proxmox) && !readOnly}
          <button class="ghost icon" title={t('Dienste automatisch finden')} aria-label={t('Dienste automatisch finden')} onclick={() => (showDiscover = true)}>{data.docker ? '🐳' : '🔍'}</button>
        {/if}
        {#if !readOnly}
          <button class="ghost icon" class:on={editMode} title={t('Anordnen & bearbeiten')} aria-label={t('Anordnen & bearbeiten')} onclick={() => (editMode = !editMode)}>
            <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 20h9" /><path d="M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4z" /></svg>
          </button>
        {/if}
        <button class="ghost icon logout" class:armed={logoutArmed} onclick={logoutClick}
          title={logoutArmed ? t('Zum Abmelden nochmal klicken') : t('Abmelden')} aria-label={logoutArmed ? t('Zum Abmelden nochmal klicken') : t('Abmelden')}>
          {#if logoutArmed}
            <span class="logout-ask">{t('Abmelden?')}</span><span class="logout-check" aria-hidden="true">✓</span>
          {:else}
            <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4" /><path d="m16 17 5-5-5-5" /><path d="M21 12H9" /></svg>
          {/if}
        </button>
        <button class="ghost icon settings-btn" title={updateStale ? t('Update verfügbar') : t('Einstellungen')} aria-label={updateStale ? t('Einstellungen – Update verfügbar') : t('Einstellungen')} onclick={() => openSettings(updateStale ? 'info' : '')}>
          {#if updateStale}<span class="update-dot" aria-hidden="true"></span>{/if}
          <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="3" /><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 1 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 1 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 1 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 1 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z" /></svg>
        </button>
      </div>
      {#if updateBubble}
        <button class="update-bubble" onclick={() => { updateBubble = false; openSettings('info'); }}>⬆ {t('Update verfügbar')}</button>
      {/if}
    </header>

    {#if showJumpBar}
      <nav class="jumpbar" class:stuck={barStuck} bind:this={jumpBarEl} aria-label={t('Zu einer Gruppe springen')}>
        <div class="jump-chips">
          {#each jumpTargets as g (g.id)}
            <button class="jump-chip" class:on={activeGroup === g.id} data-group={g.id} aria-current={activeGroup === g.id ? 'location' : undefined} onclick={() => jumpToGroup(g.id)}>{g.name}</button>
          {/each}
        </div>
      </nav>
    {/if}

    {#each data.shareRequests || [] as s (s.boardId)}
      <div class="share-ask card">
        <span class="share-icon" aria-hidden="true">{s.boardIcon || '▦'}</span>
        <span><b>{t('{name} möchte das Dashboard „{board}“ mit dir teilen.', { name: s.owner, board: t(s.boardName) })}</b>
          <span class="muted">{s.actions ? t('Du könntest es ansehen und seine Knöpfe benutzen.') : t('Du könntest es ansehen, aber nichts verändern.')}</span></span>
        <span class="spacer"></span>
        <button class="small" onclick={() => answerShare(s, false)}>{t('Ablehnen')}</button>
        <button class="small primary" onclick={() => answerShare(s, true)}>{t('Annehmen')}</button>
      </div>
    {/each}

    {#if readOnly}
      <div class="shared-note on-bg" title={t('Geteilte Dashboards kann nur ihr Besitzer ändern.')}>
        👁 {t('Geteilt von {name}', { name: board.owner })} · {board.canAct ? t('ansehen und Knöpfe benutzen') : t('nur ansehen')}
      </div>
    {/if}

    {#if editMode}
      <div class="editbar card">
        <span>✎ {t('Bearbeitungsmodus: Kacheln per Drag & Drop verschieben, anklicken zum Bearbeiten.')}</span>
        <span class="spacer"></span>
        <button class="small" onclick={addGroup}>+ {t('Gruppe')}</button>
        <button class="small primary" onclick={() => (editMode = false)}>{t('Fertig')}</button>
      </div>
    {/if}

    {#if allTiles.length === 0 && readOnly}
      <div class="empty card"><p class="muted">{t('Auf diesem geteilten Dashboard gibt es noch nichts zu sehen.')}</p></div>
    {:else if allTiles.length === 0}
      <div class="empty card">
        <h2>{t("Los geht's")} 👋</h2>
        <p class="muted">{t('Dein Dashboard ist noch leer. Füge deine Dienste hinzu – am schnellsten automatisch über Docker.')}</p>
        <div class="row center">
          {#if data.docker || data.proxmox}<button class="primary" onclick={() => (showDiscover = true)}>{data.docker ? '🐳' : '🔍'} {t('Dienste automatisch finden')}</button>{/if}
          {#if admin && !data.proxmox}<button class:primary={!!data.proxmoxHint && !data.docker} onclick={connectProxmox}>🖥️ {t('Proxmox verbinden')}</button>{/if}
          <button class:primary={!data.docker} onclick={() => (editing = { groupId: boardGroups.find((g) => !g.locked)?.id })} disabled={!boardGroups.some((g) => !g.locked)}>+ {t('Kachel hinzufügen')}</button>
          <label class="button">⇪ {t('Von Heimdall übernehmen')}<input type="file" accept="application/json,.json" onchange={async (e) => { if (await importFile(e.target.files[0], board?.id)) saved(); e.target.value = ''; }} hidden /></label>
        </div>
        <p class="help">{t('Heimdall: Einstellungen → Exportieren lädt eine HeimdallExport.json herunter. Bekannte Dienste werden samt API-Key als Live-Kachel übernommen, Tags werden zu Gruppen.')}</p>
        {#if data.proxmoxHint && !data.proxmox}
          <p class="help">{t('Heimdeck läuft auf Proxmox – verbinde es mit einem API-Token, dann werden deine VMs und Container als Dienste vorgeschlagen.')}</p>
        {:else if !data.docker && admin}
          <p class="help">{t('Tipp: Wenn du den Docker-Socket einbindest, erkennt Heimdeck deine Container automatisch (siehe docker-compose.yml). Mit Proxmox verbunden werden auch deine VMs und LXC-Container vorgeschlagen.')}</p>
        {/if}
        {#if !boardGroups.length}<button onclick={addGroup}>+ {t('Gruppe anlegen')}</button>{/if}
      </div>
    {/if}

    {#each boardGroups as group, gi (group.id)}
      {#if group.locked}
        <form class="locked card" id="group-{group.id}" class:flash={flashGroup === group.id} onsubmit={(e) => unlock(e, group)}>
          <span class="lock-name">🔒 {group.name}</span>
          <span class="spacer"></span>
          <input type="password" placeholder={t('Passwort')} bind:value={unlockPw[group.id]} autocomplete="off" aria-label={t('Passwort für {name}', { name: group.name })}
            title={readOnly ? '' : t('Gruppenpasswort vergessen? Dein Anmelde-Passwort entsperrt die Gruppe auch – danach lässt sie sich löschen oder neu schützen.')} />
          <button class="small">{t('Entsperren')}</button>
          {#if editMode}<span class="help lock-hint">{t('Gruppenpasswort vergessen? Dein Anmelde-Passwort entsperrt die Gruppe auch – danach lässt sie sich löschen oder neu schützen.')}</span>{/if}
        </form>
      {:else if group.visibility !== 'hidden' || showHidden || editMode}
        <section class="group" id="group-{group.id}" class:flash={flashGroup === group.id} aria-label={group.name} ondragover={(e) => editMode && e.preventDefault()} ondrop={(e) => editMode && groupDrop(e, group)}>
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
          {#if isFree(group) && group.tiles.length}
            {#if editMode && narrow}
              <p class="empty-group on-bg">{t('Freies Raster: auf einem größeren Bildschirm anordnen.')}</p>
            {/if}
            <FreeGrid tiles={group.tiles} {defs} {status} {editMode} {newTab} {local} cols={narrow ? 4 : 12}
              {dragging} {findTile} grab={() => grab} tileDragProps={freeDragProps} onEdit={readOnly ? null : (tile) => (editing = tile)}
              onCommit={(grid, incomingId) => commitGrid(group, grid, incomingId)} />
            {#if editMode}
              <div class="free-add">
                <button class="add" onclick={() => (editing = { groupId: group.id })}>+ {t('Kachel')}</button>
              </div>
            {/if}
          {:else}
            <div class="grid" class:uniform={isUniform(group)} class:compact={!!compactOf(group)}>
              {#if !group.tiles.length && !editMode}
                <p class="empty-group on-bg">{t('Noch keine Kacheln – über ✎ oben rechts hinzufügen.')}</p>
              {/if}
              {#each group.tiles as tile (tile.id)}
                <div class="slot" class:drop={dropTarget === tile.id && dragging !== tile.id} class:dragging={dragging === tile.id}>
                  <Tile {tile} def={defs[tile.integration]} status={status[tile.id]} {editMode} {newTab} {local} uniform={isUniform(group)} compact={compactOf(group)}
                    onEdit={readOnly ? null : () => (editing = tile)} dragProps={dragPropsFor(tile, group)} />
                </div>
              {/each}
              {#if editMode}
                <button class="add" onclick={() => (editing = { groupId: group.id })}>+ {t('Kachel')}</button>
              {/if}
            </div>
          {/if}
        </section>
      {/if}
    {/each}

    {#if hiddenCount && !showHidden && !editMode}
      <button class="hidden-hint on-bg" onclick={toggleHidden}>
        👁 {hiddenCount === 1 ? t('1 ausgeblendete Gruppe – anzeigen') : t('{n} ausgeblendete Gruppen – anzeigen', { n: hiddenCount })}
      </button>
    {/if}

    {#if allTiles.length && settings.showUptime !== 'false'}
      <Uptime tiles={allTiles} {status} onNotify={() => openSettings('notify')} />
    {/if}
    <Credit app version={data.version} update={data.update} />
  </div>

  {#if showJumpButton}
    <!-- Phones only (CSS): appears once you scrolled down, opens the groups from below. -->
    <button class="jump-fab" class:show={scrolledDown && !jumpSheet} onclick={() => (jumpSheet = true)} aria-label={t('Zu einer Gruppe springen')} title={t('Zu einer Gruppe springen')}>
      <svg viewBox="0 0 24 24" width="22" height="22" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M9 6h11M9 12h11M9 18h11" /><path d="M4 6h.01M4 12h.01M4 18h.01" /></svg>
    </button>
    {#if jumpSheet}
      <div class="backdrop jump-backdrop" role="presentation" onclick={(e) => e.target === e.currentTarget && (jumpSheet = false)}>
        <div class="jump-sheet" role="dialog" aria-modal="true" aria-label={t('Springen zu')}>
          <div class="sheet-head"><b>{t('Springen zu')}</b><button class="ghost small" onclick={() => (jumpSheet = false)}>{t('Schließen')}</button></div>
          <button class="sheet-item" onclick={() => { jumpSheet = false; scrollTo({ top: 0, behavior: 'smooth' }); }}>↑ {t('Ganz nach oben')}</button>
          {#each jumpTargets as g (g.id)}
            <button class="sheet-item" class:on={activeGroup === g.id} onclick={() => jumpToGroup(g.id)}>
              <span>{g.name}</span><span class="muted">{t('{n} Kacheln', { n: g.count })}</span>
            </button>
          {/each}
        </div>
      </div>
    {/if}
  {/if}
  {#if editing}
    <TileEditor tile={editing} groups={groupOptions} integrations={data.integrations} onClose={() => { editing = null; discoverAfterSave = false; }} onSaved={saved} />
  {/if}
  {#if season}
    {#key season}<SeasonFx {season} />{/key}
  {/if}
  {#if showDiscover}
    <Discover groups={boardGroups.filter((g) => !g.locked)} boardId={board?.id} onClose={() => (showDiscover = false)} onSaved={saved} />
  {/if}
  {#if editingGroup}
    <GroupEditor group={editingGroup} {boards} onClose={() => (editingGroup = null)} onSaved={saved} />
  {/if}
  {#if showSettings}
    <Settings {settings} {boards} boardId={board?.id} section={settingsSection} update={data.update} version={data.version} system={data.system} notify={data.notify} docker={data.docker} username={data.username} role={data.role} debug={data.debug} debugForced={data.debugForced} onSwitchBoard={switchBoard} onClose={() => (showSettings = false)} onSaved={saved} />
  {/if}
  {#if showKeys}
    <Modal title={t('Tastenkürzel')} onClose={() => (showKeys = false)}>
      <div class="keylist">
        {#each ACTIONS as a (a.id)}
          {#if keys[a.id]}<div><kbd>{keyLabel(keys[a.id])}</kbd><span>{t(a.label)}</span></div>{/if}
        {/each}
        {#if boards.length > 1}<div><kbd>1</kbd>–<kbd>{Math.min(9, boards.length)}</kbd><span>{t('Dashboard direkt öffnen')}</span></div>{/if}
      </div>
      <p class="help">{t('Ändern unter Einstellungen → Tastenkürzel. Kürzel wirken nicht, während du in ein Feld tippst.')}</p>
    </Modal>
  {/if}
{/if}

<style>
  /* Its own layer above the seasonal effects, which run behind the tiles. */
  .page { max-width: 1400px; margin: 0 auto; padding: 28px 24px 80px; display: grid; gap: 28px; position: relative; z-index: 1; }
  /* z-index lifts the header (and its dropdowns) above the cards below, which form their own layers. */
  .top { display: grid; grid-template-columns: 1fr minmax(0, 620px) 1fr; gap: 16px; align-items: center; position: relative; z-index: 30; }
  /* The brand block (with the dashboard menu) sits above the icons and search next to it. */
  .brand { min-width: 0; position: relative; z-index: 2; }
  h1 { margin: 0; font-size: 24px; letter-spacing: -.01em; }
  .greet { font-size: 13.5px; margin-top: 2px; line-height: 1.35; }
  .when { white-space: nowrap; }
  .title-row { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; }
  .boards { position: relative; }
  .board-btn { padding: 4px 10px; font-size: 13px; border-radius: 999px; display: inline-flex; gap: 6px; align-items: center; }
  .caret { font-size: 10px; color: var(--muted); }
  .board-menu { position: absolute; top: calc(100% + 6px); left: 0; z-index: 45; min-width: 180px; padding: 6px; display: grid; gap: 2px; background: var(--surface-solid); }
  .board-menu button { border: 0; background: none; text-align: left; padding: 8px 10px; border-radius: var(--r-xs); }
  .board-menu button:hover, .board-menu button.on { background: var(--surface-2); }
  .menu-label { font-size: 11px; text-transform: uppercase; letter-spacing: .06em; color: var(--muted); padding: 8px 10px 2px; border-top: 1px solid var(--border); margin-top: 4px; }
  .menu-owner { color: var(--muted); font-size: 12px; }
  /* A dashboard offered by someone else, and the note on a shared one. */
  .share-ask { display: flex; align-items: center; gap: 10px; padding: 10px 14px; font-size: 14px; flex-wrap: wrap; border-color: color-mix(in srgb, var(--accent) 55%, transparent); }
  .share-ask .muted { display: block; font-size: 12.5px; }
  .share-icon { font-size: 20px; }
  .shared-note { justify-self: start; font-size: 13px; color: var(--muted); padding: 5px 12px; border: 1px dashed var(--border-strong); border-radius: 999px; margin-top: -10px; }
  .empty-group { grid-column: 1 / -1; margin: 0; font-size: 13px; color: var(--muted); padding: 10px 14px; border: 1px dashed var(--border-strong); border-radius: var(--radius); }
  .tools { display: flex; justify-content: flex-end; gap: 4px; }
  .tools .on { background: var(--accent); color: var(--on-accent, #fff); }
  /* A newer release, out for a few days: red dot on the gear and a short hint below the icons. */
  .settings-btn { position: relative; }
  .update-dot { position: absolute; top: 4px; right: 4px; width: 9px; height: 9px; border-radius: 50%; background: var(--bad); box-shadow: 0 0 0 2px var(--surface-solid); }
  .update-bubble {
    position: absolute; right: 0; top: calc(100% + 6px); z-index: 46; padding: 7px 14px; border-radius: 999px; font-size: 13px; font-weight: 600;
    background: var(--bad); border-color: var(--bad); color: #fff; box-shadow: var(--shadow); animation: bubble .25s ease-out;
  }
  @keyframes bubble { from { opacity: 0; transform: translateY(-6px); } }
  .logout.armed { width: auto; padding: 0 10px; background: var(--bad); color: #fff; }
  .logout-ask { font-size: 12.5px; font-weight: 600; white-space: nowrap; }
  .logout-check { display: none; font-weight: 700; }
  .hidden-hint { justify-self: center; background: transparent; border: 1px dashed var(--border-strong); color: var(--muted); font-size: 13px; padding: 6px 14px; border-radius: 999px; }

  .editbar { display: flex; gap: 10px; align-items: center; padding: 10px 14px; font-size: 14px; flex-wrap: wrap; }
  .group { display: grid; gap: 12px; }
  /* Jumped to from the search: a little room above, and the title lights up briefly. */
  .group, .locked { scroll-margin-top: 16px; }
  /* With the jump bar the groups land below it. */
  .page:has(.jumpbar) :is(.group, .locked) { scroll-margin-top: 70px; }
  /* Jump bar: sticks to the top while scrolling, the group you are in is marked. */
  /* min-width: 0 keeps the long chip row from widening the page; it scrolls sideways instead. */
  .jumpbar { position: sticky; top: 0; z-index: 25; margin: -14px -8px -10px; padding: 8px; min-width: 0; }
  .jumpbar::before { content: ''; position: absolute; inset: 0; border-radius: var(--radius); background: color-mix(in srgb, var(--surface-solid) 82%, transparent); backdrop-filter: blur(12px); opacity: 0; transition: opacity .2s; pointer-events: none; }
  .jumpbar.stuck::before { opacity: 1; }
  .jump-chips { position: relative; display: flex; gap: 6px; overflow-x: auto; scrollbar-width: none; justify-content: safe center; }
  .jump-chips::-webkit-scrollbar { display: none; }
  .jump-chip { flex: none; padding: 5px 13px; font-size: 13px; border-radius: 999px; background: var(--surface); backdrop-filter: blur(10px); border: 1px solid var(--border); color: var(--muted); white-space: nowrap; }
  .jump-chip:hover { color: var(--text); border-color: var(--border-strong); }
  .jump-chip.on { background: color-mix(in srgb, var(--accent) 20%, var(--surface-solid)); border-color: var(--accent); color: var(--text); font-weight: 500; }
  /* Phone button and sheet; hidden on larger screens. */
  .jump-fab { display: none; }
  .jump-backdrop { position: fixed; inset: 0; z-index: 70; background: rgba(0, 0, 0, .5); display: flex; align-items: flex-end; }
  .jump-sheet { width: 100%; max-height: 70vh; overflow-y: auto; background: var(--surface-solid); border-radius: 18px 18px 0 0; border-top: 1px solid var(--border-strong); padding: 12px 12px calc(14px + env(safe-area-inset-bottom)); display: grid; gap: 2px; animation: sheet .2s ease-out; }
  @keyframes sheet { from { transform: translateY(40%); opacity: 0; } }
  .sheet-head { display: flex; align-items: center; justify-content: space-between; padding: 2px 6px 8px; }
  .sheet-item { display: flex; justify-content: space-between; align-items: center; gap: 10px; border: 0; background: none; text-align: left; padding: 12px 10px; border-radius: var(--r-sm); font-size: 15px; }
  .sheet-item.on { background: color-mix(in srgb, var(--accent) 16%, transparent); color: var(--accent); }
  .sheet-item .muted { font-size: 12.5px; }
  .group.flash .group-head h2 { color: var(--accent); transition: color .2s; }
  .group.flash .group-head::before { content: ''; width: 4px; height: 18px; border-radius: 2px; background: var(--accent); animation: flash 1.6s ease-out; }
  .locked.flash { border-color: var(--accent); }
  @keyframes flash { 0%, 60% { opacity: 1; } 100% { opacity: 0; } }
  .group-head { display: flex; align-items: center; gap: 8px; min-height: 30px; }
  .group-head h2 { margin: 0; font-size: 13px; text-transform: uppercase; letter-spacing: .08em; color: var(--muted); font-weight: 600; }
  .group-tools { display: flex; gap: 2px; flex-wrap: wrap; }
  .vis { font-size: 12px; color: var(--muted); }
  .locked { display: flex; align-items: center; gap: 10px; padding: 10px 14px; flex-wrap: wrap; }
  .lock-name { font-size: 13px; text-transform: uppercase; letter-spacing: .08em; color: var(--muted); font-weight: 600; }
  .locked input { width: 180px; padding: 6px 10px; }
  .lock-hint { flex-basis: 100%; }
  .net { display: inline-flex; align-items: center; gap: 6px; padding: 6px 10px; font-size: 13px; color: var(--muted); }
  .manual { color: var(--accent); }
  .grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(210px, 1fr)); gap: 12px; grid-auto-flow: dense; align-items: start; }
  /* Uniform: every tile one column of the same width; tiles in a row share the tallest one's height. */
  .grid.uniform { grid-template-columns: repeat(auto-fill, minmax(300px, 1fr)); align-items: stretch; }
  /* Compact: small square tiles with the icon. */
  .grid.compact { grid-template-columns: repeat(auto-fill, minmax(104px, 1fr)); align-items: stretch; }
  .grid.compact .add { min-height: 0; aspect-ratio: 1; }
  .keylist { display: grid; gap: 8px; }
  .keylist div { display: flex; align-items: center; gap: 8px; }
  .keylist span { margin-left: 4px; }
  kbd { display: inline-grid; place-items: center; min-width: 26px; height: 26px; padding: 0 6px; border: 1px solid var(--border-strong); border-bottom-width: 2px; border-radius: 6px; font: 600 13px ui-monospace, monospace; background: var(--surface-2); }
  .free-add { display: grid; grid-template-columns: repeat(auto-fill, minmax(210px, 1fr)); }
  .slot { display: contents; }
  .slot.drop > :global(.tile) { border-color: var(--accent); transform: translateZ(0) scale(1.02); }
  .slot.dragging > :global(.tile) { opacity: .4; }
  .add { border: 1px dashed var(--border-strong); background: transparent; min-height: 68px; border-radius: var(--radius); color: var(--muted); }
  .empty { padding: 36px 24px; text-align: center; display: grid; gap: 12px; justify-items: center; }
  .empty h2 { margin: 0; }
  .empty p { margin: 0; max-width: 520px; }
  .center { justify-content: center; flex-wrap: wrap; }

  @media (max-width: 900px) {
    /* minmax(0, …): the title block may shrink and never pushes over the icons. */
    .top { grid-template-columns: minmax(0, 1fr) auto; }
    .top :global(.search) { grid-column: 1 / -1; grid-row: 2; max-width: none; }
  }
  @media (max-width: 640px) {
    .net-label { display: none; }
    /* Title and dashboard button stay side by side in every theme: wide theme
       fonts (monospace, serif, capitals) shrink the title and shorten the
       dashboard name with "…" instead of wrapping or overlapping the icons. */
    .title-row { flex-wrap: nowrap; min-width: 0; gap: 8px; }
    h1 { font-size: clamp(18px, 6.2vw, 24px); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; min-width: 0; flex: 0 1 auto; }
    .boards { min-width: 0; flex: 0 1 auto; }
    .board-btn { max-width: 100%; min-width: 0; padding: 3px 8px; font-size: 12.5px; }
    .board-name { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
    .greet { font-size: 12.5px; }
    .when { white-space: normal; }
    /* Icons in a compact block of two rows that matches the height of the title
       block: 2 + 2 for four icons, 3 + 2 or 3 + 3 when there are more. */
    .tools { display: grid; grid-template-columns: repeat(2, 38px); justify-content: center; align-content: center; align-self: stretch; gap: 2px; }
    .tools:has(> :nth-child(5)) { grid-template-columns: repeat(3, 38px); }
    .tools :global(button) { width: 38px; height: 38px; padding: 0; justify-content: center; }
    /* No room for the question in the icon block: a check mark asks instead. */
    .logout.armed { width: 38px; padding: 0; }
    .logout-ask { display: none; }
    .logout-check { display: inline; }
    .locked { display: grid; grid-template-columns: minmax(0, 1fr); }
    .locked .spacer { display: none; }
    .locked input { width: 100%; }
    .page { padding: 16px 16px 60px; gap: 22px; }
    /* Phones: the bar scrolls away with the header; the round button takes over. */
    .jumpbar { position: relative; margin: -10px -16px -8px; padding: 0 16px; }
    .jumpbar.stuck::before { opacity: 0; }
    .jump-chips { justify-content: flex-start; }
    .page:has(.jumpbar) :is(.group, .locked) { scroll-margin-top: 12px; }
    .jump-fab {
      display: grid; place-items: center; position: fixed; right: 16px; bottom: calc(18px + env(safe-area-inset-bottom)); z-index: 50;
      width: 48px; height: 48px; padding: 0; border-radius: 50%; background: var(--accent); border-color: var(--accent); color: var(--on-accent, #fff);
      box-shadow: var(--shadow); opacity: 0; transform: translateY(12px) scale(.9); pointer-events: none; transition: opacity .2s, transform .2s;
    }
    .jump-fab.show { opacity: 1; transform: none; pointer-events: auto; }
    .grid { grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 10px; }
    .grid.uniform { grid-template-columns: minmax(0, 1fr); }
    .grid.compact { grid-template-columns: repeat(auto-fill, minmax(84px, 1fr)); }
  }
</style>
