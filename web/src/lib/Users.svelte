<script>
  // Accounts of this Heimdeck (admin only): add people by hand or with an
  // invitation link, change their role, disable, reset the password or delete.
  import { get, post, put, del } from './api.js';
  import { toast } from './toast.svelte.js';
  import { t, i18n } from './i18n.svelte.js';
  import { confirmDialog, promptDialog } from './dialog.svelte.js';

  let users = $state([]);
  let invites = $state([]);
  let me = $state(0);
  let loaded = $state(false);
  let adding = $state(false);
  let form = $state({ username: '', password: '', role: 'user' });
  let link = $state(''); // the invitation link just created – shown only once

  async function load() {
    try {
      const res = await get('/users');
      users = res.users;
      invites = res.invites;
      me = res.me;
      loaded = true;
    } catch (e) {
      toast(e.message, 'bad');
    }
  }
  load();

  const fmt = (unix) => new Date(unix * 1000).toLocaleDateString(i18n.lang === 'de' ? 'de-DE' : 'en-GB', { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' });

  async function create(e) {
    e.preventDefault();
    try {
      const u = await post('/users', $state.snapshot(form));
      toast(t('„{name}“ angelegt – Benutzername und Passwort jetzt weitergeben.', { name: u.username }));
      form = { username: '', password: '', role: 'user' };
      adding = false;
      load();
    } catch (err) {
      toast(err.message, 'bad');
    }
  }

  async function change(u, body) {
    try {
      await put(`/users/${u.id}`, body);
      load();
    } catch (err) {
      toast(err.message, 'bad');
      load();
    }
  }

  async function resetPassword(u) {
    const pw = await promptDialog(t('Neues Passwort für „{name}“ (mindestens 8 Zeichen). Das Konto wird dabei abgemeldet.', { name: u.username }), { title: t('Passwort zurücksetzen'), confirmLabel: t('Setzen') });
    if (!pw) return;
    try {
      await put(`/users/${u.id}`, { password: pw });
      toast(t('Passwort von „{name}“ geändert', { name: u.username }));
    } catch (err) {
      toast(err.message, 'bad');
    }
  }

  async function remove(u) {
    const ok = await confirmDialog(t('„{name}“ löschen? Die Dashboards dieses Kontos werden mit allen Gruppen und Kacheln mitgelöscht.', { name: u.username }), { danger: true, confirmLabel: t('Löschen') });
    if (!ok) return;
    try {
      await del(`/users/${u.id}`);
      load();
    } catch (err) {
      toast(err.message, 'bad');
    }
  }

  async function invite() {
    try {
      const res = await post('/invites');
      link = `${location.origin}/?invite=${res.token}`;
      load();
    } catch (err) {
      toast(err.message, 'bad');
    }
  }
  async function copyLink() {
    try {
      await navigator.clipboard.writeText(link);
      toast(t('Kopiert'));
    } catch {
      toast(t('Kopieren nicht möglich – bitte von Hand markieren.'), 'bad');
    }
  }
  async function dropInvite(i) {
    try {
      await del(`/invites/${i.id}`);
      load();
    } catch (err) {
      toast(err.message, 'bad');
    }
  }
</script>

<h3>{t('Benutzerverwaltung')}</h3>
<p class="help">{t('Jeder Benutzer hat eigene Dashboards, eigene Einstellungen und bekommt die Meldungen zu seinen Kacheln. Die Dashboards der anderen sieht niemand – auch du als Admin nicht –, außer sie werden geteilt.')}</p>

{#if loaded}
  <div class="users">
    {#each users as u (u.id)}
      <div class="user" class:off={u.disabled}>
        <span class="avatar" aria-hidden="true">{u.username.slice(0, 1).toUpperCase()}</span>
        <div class="who">
          <b>{u.username}</b>
          {#if u.id === me}<span class="tag">{t('du')}</span>{/if}
          {#if u.disabled}<span class="tag bad">{t('gesperrt')}</span>{/if}
        </div>
        <select value={u.role} onchange={(e) => change(u, { role: e.target.value })} disabled={u.id === me} aria-label={t('Rolle von {name}', { name: u.username })}>
          <option value="user">{t('Benutzer')}</option>
          <option value="admin">{t('Admin')}</option>
        </select>
        {#if u.id !== me}
          <div class="acts">
            <button class="small" onclick={() => resetPassword(u)}>{t('Passwort')}</button>
            <button class="small" onclick={() => change(u, { disabled: !u.disabled })}>{u.disabled ? t('Entsperren') : t('Sperren')}</button>
            <button class="small danger" onclick={() => remove(u)}>{t('Löschen')}</button>
          </div>
        {/if}
      </div>
    {/each}
  </div>
  <p class="help">{t('Admins verwalten die Benutzer und die Einstellungen für alle: Netzwerk, Docker, Proxmox-Suche, Updates und den Debug-Modus.')}</p>

  <div class="row wrap">
    <button class="primary" onclick={invite}>🔗 {t('Einladungslink erstellen')}</button>
    <button onclick={() => (adding = !adding)} aria-expanded={adding}>+ {t('Benutzer selbst anlegen')}</button>
  </div>

  {#if link}
    <div class="invite-box">
      <span class="help">{t('Schick diesen Link an die Person. Sie wählt damit Benutzername und Passwort selbst. Er gilt 7 Tage und nur einmal – und wird nur jetzt angezeigt.')}</span>
      <div class="secret-row">
        <input readonly value={link} onfocus={(e) => e.target.select()} aria-label={t('Einladungslink')} />
        <button class="small primary" onclick={copyLink}>📋 {t('Kopieren')}</button>
      </div>
    </div>
  {/if}

  {#if adding}
    <form class="add" onsubmit={create}>
      <div class="grid3">
        <!-- svelte-ignore a11y_autofocus -->
        <input bind:value={form.username} placeholder={t('Benutzername')} autocomplete="off" required autofocus />
        <input type="password" bind:value={form.password} placeholder={t('Passwort (min. 8 Zeichen)')} autocomplete="new-password" minlength="8" required />
        <select bind:value={form.role} aria-label={t('Rolle')}>
          <option value="user">{t('Benutzer')}</option>
          <option value="admin">{t('Admin')}</option>
        </select>
      </div>
      <div class="row"><span class="spacer"></span><button class="small" type="button" onclick={() => (adding = false)}>{t('Abbrechen')}</button><button class="small primary">{t('Anlegen')}</button></div>
    </form>
  {/if}

  {#if invites.length}
    <h3>{t('Offene Einladungen')}</h3>
    {#each invites as i (i.id)}
      <div class="invite">
        <span>🔗 {t('gültig bis {date}', { date: fmt(i.expires) })}{#if i.createdBy} · {t('von {name}', { name: i.createdBy })}{/if}</span>
        <button class="small" onclick={() => dropInvite(i)}>{t('Zurückziehen')}</button>
      </div>
    {/each}
  {/if}
{/if}

<style>
  .users { display: grid; gap: 6px; }
  .user { display: grid; grid-template-columns: 34px minmax(0, 1fr) auto auto; gap: 10px; align-items: center; padding: 8px 10px; border: 1px solid var(--border); border-radius: var(--r-md); background: var(--surface-2); }
  .user.off { opacity: .65; }
  .avatar { width: 34px; height: 34px; border-radius: 50%; display: grid; place-items: center; font-weight: 700; background: color-mix(in srgb, var(--accent) 22%, transparent); color: var(--accent); }
  .who { display: flex; align-items: center; gap: 6px; min-width: 0; flex-wrap: wrap; }
  .who b { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .tag { font-size: 11px; padding: 1px 7px; border-radius: 999px; background: var(--surface); border: 1px solid var(--border); color: var(--muted); }
  .tag.bad { color: var(--bad); border-color: color-mix(in srgb, var(--bad) 45%, transparent); }
  .user select { font-size: 13px; padding: 4px 8px; }
  .acts { display: flex; gap: 4px; }
  .small { font-size: 12px; }
  .wrap { flex-wrap: wrap; }
  .invite-box { display: grid; gap: 8px; padding: 12px; border: 1px solid color-mix(in srgb, var(--accent) 50%, transparent); border-radius: var(--r-md); }
  .secret-row { display: flex; gap: 8px; }
  .secret-row input { flex: 1; min-width: 0; font-size: 13px; }
  .add { display: grid; gap: 8px; }
  .grid3 { display: grid; grid-template-columns: 1fr 1fr auto; gap: 8px; }
  .invite { display: flex; align-items: center; gap: 8px; justify-content: space-between; font-size: 13.5px; }
  @media (max-width: 640px) {
    .user { grid-template-columns: 34px minmax(0, 1fr) auto; }
    .acts { grid-column: 1 / -1; justify-content: flex-end; }
    .grid3 { grid-template-columns: 1fr; }
  }
</style>
