<script>
  import { put, del } from './api.js';
  import { toast } from './toast.svelte.js';
  import Modal from './Modal.svelte';

  let { group, onClose, onSaved } = $props();

  // svelte-ignore state_referenced_locally
  let name = $state(group.name);
  // svelte-ignore state_referenced_locally
  let visibility = $state(group.visibility || 'visible');
  let password = $state('');
  let saving = $state(false);
  // svelte-ignore state_referenced_locally
  let hadPassword = group.visibility === 'protected';

  const options = [
    { id: 'visible', label: 'Immer sichtbar', help: 'Ganz normal auf dem Dashboard.' },
    { id: 'hidden', label: 'Ausgeblendet', help: 'Erscheint erst nach Klick auf 👁 oben rechts – gilt nur für diese Browser-Sitzung.' },
    { id: 'protected', label: 'Mit Passwort geschützt', help: 'Zeigt nur eine gesperrte Leiste. Nach Eingabe des Passworts bleibt die Gruppe 12 Stunden bzw. bis zum Abmelden offen.' },
  ];

  async function save(e) {
    e?.preventDefault();
    saving = true;
    try {
      await put(`/groups/${group.id}`, { name, visibility, password });
      toast('Gruppe gespeichert');
      onSaved();
    } catch (err) {
      toast(err.message, 'bad');
    }
    saving = false;
  }

  async function remove() {
    if (!confirm(`Gruppe „${group.name}“${group.tiles.length ? ` mit ${group.tiles.length} Kachel(n)` : ''} löschen?`)) return;
    try {
      await del(`/groups/${group.id}`);
      onSaved();
    } catch (err) {
      toast(err.message, 'bad');
    }
  }
</script>

<Modal title="Gruppe bearbeiten" {onClose}>
  <form id="group-form" class="form" onsubmit={save}>
    <label class="field"><span>Name</span><input bind:value={name} required /></label>
    <div class="field">
      <span>Sichtbarkeit</span>
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
        <span>{hadPassword ? 'Neues Passwort (leer lassen zum Beibehalten)' : 'Passwort für diese Gruppe'}</span>
        <input type="password" bind:value={password} autocomplete="new-password" minlength="4" required={!hadPassword} />
      </label>
    {/if}
  </form>

  {#snippet footer()}
    <button class="danger" onclick={remove}>Löschen</button>
    <span class="spacer"></span>
    <button onclick={onClose}>Abbrechen</button>
    <button class="primary" type="submit" form="group-form" disabled={saving}>Speichern</button>
  {/snippet}
</Modal>

<style>
  .form { display: grid; gap: 14px; }
  .field { display: grid; gap: 6px; font-size: 13px; color: var(--muted); }
  .opt { display: flex; gap: 10px; align-items: flex-start; padding: 10px 12px; border: 1px solid var(--border); border-radius: 12px; cursor: pointer; color: var(--text); font-size: 14px; }
  .opt.on { border-color: var(--accent); }
  .opt input { margin-top: 3px; }
</style>
