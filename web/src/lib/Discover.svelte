<script>
  import { get, post } from './api.js';
  import { toast } from './toast.svelte.js';
  import Modal from './Modal.svelte';
  import Icon from './Icon.svelte';
  import { t } from './i18n.svelte.js';

  // Lists running Docker containers and turns the selected ones into tiles.
  let { groups, boardId, onClose, onSaved } = $props();

  let items = $state(null);
  let error = $state('');
  let selected = $state({});
  let busy = $state(false);

  (async () => {
    try {
      items = await get('/discover');
      for (const it of items) {
        const s = it.suggestion;
        selected[s.container.id] = !it.exists && s.url && s.integration !== 'link';
      }
    } catch (e) {
      error = e.message;
    }
  })();

  let count = $derived(Object.values(selected).filter(Boolean).length);

  async function add() {
    busy = true;
    const groupIds = Object.fromEntries(groups.map((g) => [g.name.toLowerCase(), g.id]));
    let added = 0;
    try {
      for (const it of items) {
        const s = it.suggestion;
        if (!selected[s.container.id]) continue;
        const groupName = s.group || t('Sonstiges');
        let gid = groupIds[groupName.toLowerCase()];
        if (!gid) {
          gid = (await post('/groups', { name: groupName, boardId })).id;
          groupIds[groupName.toLowerCase()] = gid;
        }
        await post('/tiles', { groupId: gid, name: s.name, url: s.url, icon: s.icon, integration: s.integration, showWidget: true });
        added++;
      }
      toast(t('{n} Kachel(n) hinzugefügt – API-Keys kannst du direkt auf den Kacheln nachtragen', { n: added }));
      onSaved();
    } catch (e) {
      toast(e.message, 'bad');
      if (added) onSaved();
    }
    busy = false;
  }
</script>

<Modal title={t('Container automatisch finden')} {onClose} wide>
  {#if error}
    <p class="bad">{error}</p>
  {:else if !items}
    <p class="muted">{t('Suche Container …')}</p>
  {:else if items.length === 0}
    <p class="muted">{t('Keine Container gefunden.')}</p>
  {:else}
    <p class="help">{t('Erkannte Dienste sind bereits vorausgewählt. Adresse und Name kannst du danach jederzeit ändern.')}</p>
    <div class="list">
      {#each items as it (it.suggestion.container.id)}
        {@const s = it.suggestion}
        <label class="item" class:dim={it.exists}>
          <input type="checkbox" bind:checked={selected[s.container.id]} />
          <Icon icon={s.icon} name={s.name} size={32} />
          <div class="grow">
            <div class="row">
              <b>{s.name}</b>
              {#if s.integration !== 'link'}<span class="tag">{t('erkannt')}</span>{/if}
              {#if it.exists}<span class="tag muted">{t('schon vorhanden')}</span>{/if}
            </div>
            <div class="help">{s.container.image} · {s.url || t('kein Port freigegeben')}</div>
          </div>
          <span class="state {s.container.state}">{s.container.state === 'running' ? t('läuft') : s.container.state}</span>
        </label>
      {/each}
    </div>
  {/if}

  {#snippet footer()}
    <span class="spacer"></span>
    <button onclick={onClose}>{t('Abbrechen')}</button>
    <button class="primary" disabled={!count || busy} onclick={add}>{t('{n} hinzufügen', { n: count })}</button>
  {/snippet}
</Modal>

<style>
  .list { display: grid; gap: 6px; }
  .item { display: flex; gap: 12px; align-items: center; padding: 10px 12px; border: 1px solid var(--border); border-radius: 12px; cursor: pointer; }
  .item:hover { border-color: var(--border-strong); }
  .dim { opacity: .55; }
  .grow { flex: 1; min-width: 0; }
  .help { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .tag { font-size: 11px; padding: 1px 6px; border-radius: 6px; background: color-mix(in srgb, var(--accent) 18%, transparent); color: var(--accent); }
  .tag.muted { background: var(--surface-2); color: var(--muted); }
  .state { font-size: 12px; color: var(--muted); }
  .state.running { color: var(--ok); }
</style>
