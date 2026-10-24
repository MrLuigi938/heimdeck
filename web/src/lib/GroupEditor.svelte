<script>
  import { put, del } from './api.js';
  import { toast } from './toast.svelte.js';
  import Modal from './Modal.svelte';
  import { t } from './i18n.svelte.js';
  import { confirmDialog } from './dialog.svelte.js';

  let { group, boards = [], onClose, onSaved } = $props();

  // svelte-ignore state_referenced_locally
  let name = $state(group.name);
  // svelte-ignore state_referenced_locally
  let visibility = $state(group.visibility || 'visible');
  let password = $state('');
  // One choice for tile size and arrangement; the free grid keeps the stored tile size
  // so switching back restores it.
  // svelte-ignore state_referenced_locally
  let look = $state(group.arrange === 'free' ? 'free' : group.layout || '');
  // svelte-ignore state_referenced_locally
  let boardId = $state(group.boardId);
  let saving = $state(false);
  // svelte-ignore state_referenced_locally
  let hadPassword = group.visibility === 'protected';

  let options = $derived([
    { id: 'visible', label: t('Immer sichtbar'), help: t('Ganz normal auf dem Dashboard.') },
    { id: 'hidden', label: t('Ausgeblendet'), help: t('Erscheint erst nach Klick auf 👁 oben rechts – gilt nur für diese Browser-Sitzung.') },
    { id: 'protected', label: t('Mit Passwort geschützt'), help: t('Zeigt nur eine gesperrte Leiste. Nach Eingabe des Passworts bleibt die Gruppe 12 Stunden bzw. bis zum Abmelden offen.') },
  ]);

  async function save(e) {
    e?.preventDefault();
    saving = true;
    try {
      const free = look === 'free';
      const layout = free ? group.layout || '' : look;
      await put(`/groups/${group.id}`, { name, visibility, password, layout, arrange: free ? 'free' : '', boardId });
      toast(t('Gruppe gespeichert'));
      onSaved();
    } catch (err) {
      toast(err.message, 'bad');
    }
    saving = false;
  }

  async function remove() {
    const msg = group.tiles.length
      ? t('Gruppe „{name}“ mit {n} Kachel(n) löschen?', { name: group.name, n: group.tiles.length })
      : t('Gruppe „{name}“ löschen?', { name: group.name });
    if (!(await confirmDialog(msg, { danger: true }))) return;
    try {
      await del(`/groups/${group.id}`);
      onSaved();
    } catch (err) {
      toast(err.message, 'bad');
    }
  }
</script>

<Modal title={t('Gruppe bearbeiten')} {onClose}>
  <form id="group-form" class="form" onsubmit={save}>
    <label class="field"><span>{t('Name')}</span><input bind:value={name} required /></label>
    <div class="field">
      <span>{t('Sichtbarkeit')}</span>
      {#each options as o}
        <label class="opt" class:on={visibility === o.id}>
          <input type="radio" name="vis" value={o.id} bind:group={visibility} />
          <div>
            <div>{o.label}</div>
            <div class="help">{o.help}</div>
          </div>
        </label>
      {/each}
    </div>
    {#if visibility === 'protected'}
      <label class="field">
        <span>{hadPassword ? t('Neues Passwort (leer lassen zum Beibehalten)') : t('Passwort für diese Gruppe')}</span>
        <input type="password" bind:value={password} autocomplete="new-password" minlength="4" required={!hadPassword} />
      </label>
    {/if}
    <label class="field">
      <span>{t('Darstellung')}</span>
      <select bind:value={look}>
        <option value="">{t('Wie in den Einstellungen')}</option>
        <option value="uniform">{t('Einheitlich – alle Kacheln gleich groß')}</option>
        <option value="auto">{t('Automatisch – Kacheln mit Live-Daten breiter')}</option>
        <option value="icons">{t('Kompakt – Icon mit Namen darunter')}</option>
        <option value="icons-hover">{t('Kompakt – nur Icon, Name beim Darüberfahren')}</option>
        <option value="free">{t('Freies Raster – Kacheln frei platzieren und in der Größe ändern')}</option>
      </select>
      {#if look === 'free'}<span class="help">{t('Im Bearbeitungsmodus (✎) Kacheln an die gewünschte Stelle ziehen und über die Ecke unten rechts größer oder kleiner ziehen (Doppelklick passt die Höhe an den Inhalt an). Eine Spalte breit wird eine Kachel zum Icon. Auf dem Handy wird das Raster auf 4 Spalten verkleinert.')}</span>{/if}
    </label>
    {#if boards.length > 1}
      <label class="field">
        <span>{t('Dashboard')}</span>
        <select bind:value={boardId}>
          {#each boards as b (b.id)}<option value={b.id}>{b.icon} {t(b.name)}</option>{/each}
        </select>
      </label>
    {/if}
  </form>

  {#snippet footer()}
    <button class="danger" onclick={remove}>{t('Löschen')}</button>
    <span class="spacer"></span>
    <button onclick={onClose}>{t('Abbrechen')}</button>
    <button class="primary" type="submit" form="group-form" disabled={saving}>{t('Speichern')}</button>
  {/snippet}
</Modal>

<style>
  .form { display: grid; gap: 14px; }
  .field { display: grid; gap: 6px; font-size: 13px; color: var(--muted); }
  .opt { display: flex; gap: 10px; align-items: flex-start; padding: 10px 12px; border: 1px solid var(--border); border-radius: var(--r-md); cursor: pointer; color: var(--text); font-size: 14px; }
  .opt.on { border-color: var(--accent); }
  .opt input { margin-top: 3px; }
</style>
