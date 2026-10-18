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
  let allTiles = $derived((data?.groups || []).flatMap((g) => g.tiles));
  let newTab = $derived(settings.openInNewTab !== 'false');
  let hasSearchServices = $derived(allTiles.some((t) => defs[t.integration]?.hasSearch));
  let openGroups = $derived((data?.groups || []).filter((g) => !g.locked));
  let hasHidden = $derived((data?.groups || []).some((g) => g.visibility === 'hidden'));
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
      document.body.style.backgroundImage = `url("${settings.background.replace(/"/g, '%22')}")`;
      document.body.classList.add('has-bg');
    } else {
      document.body.style.backgroundImage = '';
      document.body.classList.remove('has-bg');
    }
    return () => media.removeEventListener('change', apply);
  });

  let greeting = $derived.by(() => {
    const h = now.getHours();
    const who = data?.username ? `, ${data.username}` : '';
    if (h < 5) return `Gute Nacht${who}`;
    if (h < 11) return `Guten Morgen${who}`;
    if (h < 18) return `Hallo${who}`;
    return `Guten Abend${who}`;
  });
  let dateText = $derived(now.toLocaleDateString('de-DE', { weekday: 'long', day: 'numeric', month: 'long' }));
  let timeText = $derived(now.toLocaleTimeString('de-DE', { hour: '2-digit', minute: '2-digit' }));

  // ---- groups ----
  async function addGroup() {
    const name = prompt('Name der neuen Gruppe');
    if (!name?.trim()) return;
    await post('/groups', { name });
    load();
  }
  function moveGroup(i, dir) {
    const gs = data.groups;
    const j = i + dir;
    if (j < 0 || j >= gs.length) return;
    [gs[i], gs[j]] = [gs[j], gs[i]];
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

  function saved() {
    editing = null;
    editingGroup = null;
    showDiscover = false;
    showSettings = false;
    load().then(loadStatus);
    // The server re-checks a saved tile right away; pick the result up shortly after.
    setTimeout(loadStatus, 4000);
  }
</script>

{#if data}
  <div class="page">
    <header class="top">
      <div class="brand">
        <h1>{settings.title || 'Heimdeck'}</h1>
        {#if settings.showGreeting !== 'false'}
          <div class="muted greet">{greeting} · {dateText} · {timeText}</div>
        {/if}
      </div>
      <Search tiles={allTiles} {newTab} searchEngine={settings.searchEngine} {hasSearchServices} {local} />
      <div class="tools">
        {#if hasExternal}
          <button class="ghost net" onclick={toggleNetwork}
            title={`${local ? 'Zuhause: lokale Adressen werden geöffnet' : 'Unterwegs: öffentliche Adressen werden geöffnet'}${network.override ? ' (manuell gewählt)' : ' (automatisch erkannt)'} – klicken zum Umschalten`}>
            {local ? '🏠' : '🌐'}<span class="net-label">{local ? 'Zuhause' : 'Unterwegs'}</span>{#if network.override}<span class="manual">•</span>{/if}
          </button>
        {/if}
        {#if hasHidden}
          <button class="ghost icon" class:on={showHidden} title={showHidden ? 'Ausgeblendete Gruppen verbergen' : 'Ausgeblendete Gruppen anzeigen'} onclick={toggleHidden}>👁</button>
        {/if}
        {#if data.docker}
          <button class="ghost icon" title="Container automatisch finden" onclick={() => (showDiscover = true)}>🐳</button>
        {/if}
        <button class="ghost icon" class:on={editMode} title="Anordnen & bearbeiten" onclick={() => (editMode = !editMode)}>✎</button>
        <button class="ghost icon" title="Einstellungen" onclick={() => (showSettings = true)}>⚙</button>
      </div>
    </header>

    {#if editMode}
      <div class="editbar card">
        <span>✎ Bearbeitungsmodus: Kacheln per Drag & Drop verschieben, anklicken zum Bearbeiten.</span>
        <span class="spacer"></span>
        <button class="small" onclick={addGroup}>+ Gruppe</button>
        <button class="small primary" onclick={() => (editMode = false)}>Fertig</button>
      </div>
    {/if}

    {#if allTiles.length === 0}
      <div class="empty card">
        <h2>Los geht's 👋</h2>
        <p class="muted">Dein Dashboard ist noch leer. Füge deine Dienste hinzu – am schnellsten automatisch über Docker.</p>
        <div class="row center">
          {#if data.docker}<button class="primary" onclick={() => (showDiscover = true)}>🐳 Container automatisch finden</button>{/if}
          <button class:primary={!data.docker} onclick={() => (editing = { groupId: data.groups[0]?.id })} disabled={!data.groups.length}>+ Kachel hinzufügen</button>
        </div>
        {#if !data.docker}
          <p class="help">Tipp: Wenn du den Docker-Socket einbindest, erkennt Heimdeck deine Container automatisch (siehe docker-compose.yml).</p>
        {/if}
        {#if !data.groups.length}<button onclick={addGroup}>+ Gruppe anlegen</button>{/if}
      </div>
    {/if}

    {#each data.groups as group, gi (group.id)}
      {#if group.locked}
        <form class="locked card" onsubmit={(e) => unlock(e, group)}>
          <span class="lock-name">🔒 {group.name}</span>
          <span class="spacer"></span>
          <input type="password" placeholder="Passwort" bind:value={unlockPw[group.id]} autocomplete="off" aria-label="Passwort für {group.name}" />
          <button class="small">Entsperren</button>
        </form>
      {:else if (group.tiles.length || editMode) && (group.visibility !== 'hidden' || showHidden || editMode)}
        <section class="group" aria-label={group.name} ondragover={(e) => editMode && e.preventDefault()} ondrop={(e) => editMode && groupDrop(e, group)}>
          <div class="group-head">
            <h2>{group.name}</h2>
            {#if group.visibility === 'hidden'}<span class="vis" title="Ausgeblendete Gruppe">👁 ausgeblendet</span>{/if}
            {#if group.visibility === 'protected'}
              <button class="ghost small vis" title="Gruppe wieder sperren" onclick={() => lock(group)}>🔓 sperren</button>
            {/if}
            {#if editMode}
              <div class="group-tools">
                <button class="ghost small" onclick={() => moveGroup(gi, -1)} disabled={gi === 0} title="Nach oben">↑</button>
                <button class="ghost small" onclick={() => moveGroup(gi, 1)} disabled={gi === data.groups.length - 1} title="Nach unten">↓</button>
                <button class="ghost small" onclick={() => (editingGroup = group)}>Bearbeiten</button>
              </div>
            {/if}
          </div>
          <div class="grid">
            {#each group.tiles as tile (tile.id)}
              <div class="slot" class:drop={dropTarget === tile.id && dragging !== tile.id} class:dragging={dragging === tile.id}>
                <Tile {tile} def={defs[tile.integration]} status={status[tile.id]} {editMode} {newTab} {local}
                  onEdit={() => (editing = tile)} dragProps={dragPropsFor(tile, group)} />
              </div>
            {/each}
            {#if editMode}
              <button class="add" onclick={() => (editing = { groupId: group.id })}>+ Kachel</button>
            {/if}
          </div>
        </section>
      {/if}
    {/each}

    {#if allTiles.length && settings.showUptime !== 'false'}
      <Uptime tiles={allTiles} {status} />
    {/if}
  </div>

  {#if editing}
    <TileEditor tile={editing} groups={openGroups} integrations={data.integrations} onClose={() => (editing = null)} onSaved={saved} />
  {/if}
  {#if showDiscover}
    <Discover groups={openGroups} onClose={() => (showDiscover = false)} onSaved={saved} />
  {/if}
  {#if editingGroup}
    <GroupEditor group={editingGroup} onClose={() => (editingGroup = null)} onSaved={saved} />
  {/if}
  {#if showSettings}
    <Settings {settings} username={data.username} onClose={() => (showSettings = false)} onSaved={saved} />
  {/if}
{/if}

<style>
  .page { max-width: 1400px; margin: 0 auto; padding: 28px 24px 80px; display: grid; gap: 28px; }
  .top { display: grid; grid-template-columns: 1fr minmax(0, 620px) 1fr; gap: 16px; align-items: center; }
  .brand { min-width: 0; }
  h1 { margin: 0; font-size: 24px; letter-spacing: -.01em; }
  .greet { font-size: 13.5px; margin-top: 2px; }
  .tools { display: flex; justify-content: flex-end; gap: 4px; }
  .tools .on { background: var(--accent); color: #fff; }
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
  .slot { display: contents; }
  .slot.drop > :global(.tile) { border-color: var(--accent); transform: scale(1.02); }
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
    .page { padding: 16px 16px 60px; gap: 22px; }
    .grid { grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 10px; }
  }
</style>
