<script>
  // Sharing dashboards: offer your own to other accounts (they have to accept;
  // they can look but not change anything, buttons only if you allow it), and
  // the dashboards others shared with you.
  import { get, put, del, post } from './api.js';
  import { toast } from './toast.svelte.js';
  import { t } from './i18n.svelte.js';
  import { confirmDialog } from './dialog.svelte.js';

  let { boards = [], groups = [], onSaved, onSwitchBoard, onClose } = $props();

  let people = $state([]);
  let incoming = $state([]);
  let outgoing = $state([]);
  let loaded = $state(false);
  let picking = $state(0); // board whose "share with" choice is open
  let groupsIn = $state([]);
  let groupsOut = $state([]);
  // Sharing single groups of one dashboard: which groups, with whom, buttons or not.
  let groupPick = $state({ board: 0, groups: [], user: 0, actions: false });

  async function load() {
    try {
      const [p, s] = await Promise.all([get('/people'), get('/shares')]);
      people = p;
      incoming = s.incoming;
      outgoing = s.outgoing;
      groupsIn = s.groupsIncoming || [];
      groupsOut = s.groupsOutgoing || [];
      loaded = true;
    } catch (e) {
      toast(e.message, 'bad');
    }
  }
  load();

  const sharesOf = (b) => outgoing.filter((s) => s.boardId === b.id);
  const notYet = (b) => people.filter((p) => !sharesOf(b).some((s) => s.userId === p.id));

  async function share(b, p) {
    picking = 0;
    try {
      await put(`/boards/${b.id}/shares/${p.id}`, { actions: false });
      toast(t('„{board}“ an {name} freigegeben – sobald angenommen, erscheint es dort.', { board: t(b.name), name: p.username }));
      load();
    } catch (e) {
      toast(e.message, 'bad');
    }
  }
  async function setActions(s, on) {
    try {
      await put(`/boards/${s.boardId}/shares/${s.userId}`, { actions: on });
      load();
    } catch (e) {
      toast(e.message, 'bad');
    }
  }
  async function unshare(s) {
    const ok = await confirmDialog(t('Freigabe von „{board}“ für {name} beenden?', { board: t(s.boardName), name: s.username }), { confirmLabel: t('Beenden') });
    if (!ok) return;
    try {
      await del(`/boards/${s.boardId}/shares/${s.userId}`);
      load();
    } catch (e) {
      toast(e.message, 'bad');
    }
  }

  const groupsOf = (b) => groups.filter((g) => g.boardId === b.id);
  const groupSharesOf = (b) => groupsOut.filter((s) => s.boardId === b.id);
  function openGroupPick(b) {
    groupPick = groupPick.board === b.id ? { board: 0, groups: [], user: 0, actions: false } : { board: b.id, groups: [], user: people[0]?.id || 0, actions: false };
  }
  function toggleGroup(id, on) {
    groupPick.groups = on ? [...groupPick.groups, id] : groupPick.groups.filter((g) => g !== id);
  }
  async function shareGroups() {
    const p = people.find((x) => x.id === Number(groupPick.user));
    if (!p || !groupPick.groups.length) return;
    try {
      await put('/groups/shares', { groups: groupPick.groups, user: p.id, actions: groupPick.actions });
      toast(t('{n} Gruppen an {name} freigegeben – sobald angenommen, erscheinen sie dort.', { n: groupPick.groups.length, name: p.username }));
      groupPick = { board: 0, groups: [], user: 0, actions: false };
      load();
    } catch (e) {
      toast(e.message, 'bad');
    }
  }
  async function setGroupActions(s, on) {
    try {
      await put('/groups/shares', { groups: [s.groupId], user: s.userId, actions: on });
      load();
    } catch (e) {
      toast(e.message, 'bad');
    }
  }
  async function unshareGroup(s, mine = true) {
    const ok = await confirmDialog(mine ? t('Freigabe der Gruppe „{group}“ für {name} beenden?', { group: s.groupName, name: s.username }) : t('Gruppe „{group}“ von {name} nicht mehr anzeigen?', { group: s.groupName, name: s.owner }), { confirmLabel: t('Beenden') });
    if (!ok) return;
    try {
      await del(`/groups/${s.groupId}/shares/${s.userId}`);
      await load();
      if (!mine) onSaved({ keepOpen: true });
    } catch (e) {
      toast(e.message, 'bad');
    }
  }
  async function acceptGroup(s) {
    try {
      await post(`/groups/${s.groupId}/accept`);
      await load();
      onSaved({ keepOpen: true });
    } catch (e) {
      toast(e.message, 'bad');
    }
  }

  async function accept(s) {
    try {
      await post(`/boards/${s.boardId}/accept`);
      toast(t('„{board}“ ist jetzt in deiner Dashboard-Liste.', { board: t(s.boardName) }));
      await load();
      onSaved({ keepOpen: true });
    } catch (e) {
      toast(e.message, 'bad');
    }
  }
  async function leave(s) {
    if (s.accepted) {
      const ok = await confirmDialog(t('„{board}“ von {name} nicht mehr anzeigen?', { board: t(s.boardName), name: s.owner }), { confirmLabel: t('Entfernen') });
      if (!ok) return;
    }
    try {
      await del(`/boards/${s.boardId}/shares/${s.userId}`);
      await load();
      onSaved({ keepOpen: true });
    } catch (e) {
      toast(e.message, 'bad');
    }
  }
</script>

<svelte:window onclick={(e) => picking && !e.target.closest('.pick') && (picking = 0)} />

{#if loaded}
  {#if incoming.length}
    <h3>{t('Mit dir geteilt')}</h3>
    {#each incoming as s (s.boardId)}
      <div class="share in" class:pending={!s.accepted}>
        <span class="b-icon" aria-hidden="true">{s.boardIcon || '▦'}</span>
        <div class="what">
          <b>{t(s.boardName)}</b>
          <span class="help">{t('von {name}', { name: s.owner })} · {s.accepted ? (s.actions ? t('ansehen und Knöpfe benutzen') : t('nur ansehen')) : t('wartet auf deine Zustimmung')}</span>
        </div>
        <div class="acts">
          {#if s.accepted}
            <button class="small" onclick={() => { onSwitchBoard(s.boardId); onClose(); }}>{t('Öffnen')}</button>
            <button class="small" onclick={() => leave(s)}>{t('Entfernen')}</button>
          {:else}
            <button class="small primary" onclick={() => accept(s)}>{t('Annehmen')}</button>
            <button class="small" onclick={() => leave(s)}>{t('Ablehnen')}</button>
          {/if}
        </div>
      </div>
    {/each}
  {/if}

  {#if groupsIn.length}
    {#if !incoming.length}<h3>{t('Mit dir geteilt')}</h3>{/if}
    {#each groupsIn as s (s.groupId)}
      <div class="share in" class:pending={!s.accepted}>
        <span class="b-icon" aria-hidden="true">👥</span>
        <div class="what">
          <b>{s.groupName}</b>
          <span class="help">{t('Gruppe von {name}', { name: s.owner })} · {s.accepted ? (s.actions ? t('ansehen und Knöpfe benutzen') : t('nur ansehen')) : t('wartet auf deine Zustimmung')}</span>
        </div>
        <div class="acts">
          {#if s.accepted}
            <button class="small" onclick={() => { onSwitchBoard(-s.ownerId); onClose(); }}>{t('Öffnen')}</button>
            <button class="small" onclick={() => unshareGroup(s, false)}>{t('Entfernen')}</button>
          {:else}
            <button class="small primary" onclick={() => acceptGroup(s)}>{t('Annehmen')}</button>
            <button class="small" onclick={() => unshareGroup(s, false)}>{t('Ablehnen')}</button>
          {/if}
        </div>
      </div>
    {/each}
  {/if}

  <h3>{t('Teilen')}</h3>
  {#if !people.length}
    <p class="help">{t('Noch niemand zum Teilen da. Ein Admin kann unter „Benutzerverwaltung“ weitere Personen einladen.')}</p>
  {:else}
    <p class="help">{t('Geteilte Dashboards erscheinen bei der anderen Person erst, wenn sie zustimmt. Sie kann nichts verändern – Knöpfe wie Schalten oder Pausieren nur, wenn du sie erlaubst. Ausfall-Meldungen bekommst nur du.')}</p>
    {#each boards as b (b.id)}
      <div class="board">
        <div class="b-head">
          <span class="b-icon" aria-hidden="true">{b.icon || '▦'}</span>
          <b>{t(b.name)}</b>
          <span class="spacer"></span>
          {#if groupsOf(b).length}
            <button class="small" class:on={groupPick.board === b.id} onclick={() => openGroupPick(b)} aria-expanded={groupPick.board === b.id}>▦ {t('Gruppen teilen …')}</button>
          {/if}
          {#if notYet(b).length}
            <div class="pick">
              <button class="small" onclick={() => (picking = picking === b.id ? 0 : b.id)} aria-expanded={picking === b.id}>👥 {t('Teilen mit …')}</button>
              {#if picking === b.id}
                <div class="menu card" role="menu">
                  {#each notYet(b) as p (p.id)}
                    <button role="menuitem" onclick={() => share(b, p)}>{p.username}</button>
                  {/each}
                </div>
              {/if}
            </div>
          {/if}
        </div>
        {#if groupPick.board === b.id}
          <div class="group-pick">
            <span class="help">{t('Nur diese Gruppen teilen – der Rest des Dashboards bleibt privat:')}</span>
            <div class="group-list">
              {#each groupsOf(b) as g (g.id)}
                <label class="check"><input type="checkbox" checked={groupPick.groups.includes(g.id)} onchange={(e) => toggleGroup(g.id, e.currentTarget.checked)} /> {g.name}</label>
              {/each}
            </div>
            <div class="row">
              <select bind:value={groupPick.user} aria-label={t('Mit wem?')}>
                {#each people as p (p.id)}<option value={p.id}>{p.username}</option>{/each}
              </select>
              <label class="check small-check"><input type="checkbox" bind:checked={groupPick.actions} /> {t('darf Knöpfe benutzen')}</label>
              <span class="spacer"></span>
              <button class="small primary" disabled={!groupPick.groups.length} onclick={shareGroups}>{groupPick.groups.length ? t('{n} Gruppen teilen', { n: groupPick.groups.length }) : t('Gruppen wählen')}</button>
            </div>
          </div>
        {/if}
        {#each groupSharesOf(b) as s (s.groupId + ':' + s.userId)}
          <div class="share">
            <span class="avatar" aria-hidden="true">{s.username.slice(0, 1).toUpperCase()}</span>
            <div class="what">
              <b>{s.username} · ▦ {s.groupName}</b>
              <span class="help">{s.accepted ? '✓ ' + t('angenommen') : '⏳ ' + t('wartet auf Zustimmung')}</span>
            </div>
            <label class="check small-check"><input type="checkbox" checked={s.actions} onchange={(e) => setGroupActions(s, e.target.checked)} /> {t('darf Knöpfe benutzen')}</label>
            <button class="small ghost" onclick={() => unshareGroup(s)} title={t('Freigabe beenden')} aria-label={t('Freigabe beenden')}>✕</button>
          </div>
        {/each}
        {#each sharesOf(b) as s (s.userId)}
          <div class="share">
            <span class="avatar" aria-hidden="true">{s.username.slice(0, 1).toUpperCase()}</span>
            <div class="what">
              <b>{s.username}</b>
              <span class="help">{s.accepted ? '✓ ' + t('angenommen') : '⏳ ' + t('wartet auf Zustimmung')}</span>
            </div>
            <label class="check small-check"><input type="checkbox" checked={s.actions} onchange={(e) => setActions(s, e.target.checked)} /> {t('darf Knöpfe benutzen')}</label>
            <button class="small ghost" onclick={() => unshare(s)} title={t('Freigabe beenden')} aria-label={t('Freigabe beenden')}>✕</button>
          </div>
        {/each}
      </div>
    {/each}
  {/if}
{/if}

<style>
  .board { display: grid; gap: 6px; padding: 10px 12px; border: 1px solid var(--border); border-radius: var(--r-md); }
  .b-head { display: flex; align-items: center; gap: 8px; }
  .b-head { flex-wrap: wrap; }
  .group-pick { display: grid; gap: 8px; padding: 10px; border-radius: var(--r-sm); background: var(--surface-2); }
  .group-list { display: flex; flex-wrap: wrap; gap: 6px 16px; }
  .group-pick .row { display: flex; gap: 8px; align-items: center; flex-wrap: wrap; }
  .group-pick select { width: auto; }
  .on { border-color: var(--accent); color: var(--accent); }
  .b-icon { width: 22px; text-align: center; }
  .share { display: grid; grid-template-columns: 30px minmax(0, 1fr) auto auto; gap: 10px; align-items: center; padding: 6px 4px 0; }
  .share.in { grid-template-columns: 30px minmax(0, 1fr) auto; padding: 8px 10px; border: 1px solid var(--border); border-radius: var(--r-md); background: var(--surface-2); }
  .share.in.pending { border-color: color-mix(in srgb, var(--accent) 55%, transparent); }
  .avatar { width: 30px; height: 30px; border-radius: 50%; display: grid; place-items: center; font-weight: 700; font-size: 13px; background: color-mix(in srgb, var(--accent) 22%, transparent); color: var(--accent); }
  .what { display: grid; min-width: 0; }
  .what b { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .what .help { font-size: 12.5px; }
  .acts { display: flex; gap: 4px; }
  .small { font-size: 12px; }
  .small-check { font-size: 13px; white-space: nowrap; }
  .pick { position: relative; }
  .menu { position: absolute; right: 0; top: calc(100% + 4px); z-index: 5; min-width: 160px; padding: 6px; display: grid; gap: 2px; background: var(--surface-solid); }
  .menu button { border: 0; background: none; text-align: left; padding: 7px 10px; border-radius: var(--r-xs); }
  .menu button:hover { background: var(--surface-2); }
  @media (max-width: 640px) {
    .share { grid-template-columns: 30px minmax(0, 1fr) auto; }
    .small-check { grid-column: 2 / -1; }
    .share.in .acts { grid-column: 1 / -1; justify-content: flex-end; }
  }
</style>
