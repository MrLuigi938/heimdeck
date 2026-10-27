<script>
  import Credit from './Credit.svelte';
  import { get, post } from './api.js';
  import { t, i18n, setLang } from './i18n.svelte.js';

  // invite: the token of an invitation link – then the form creates an account.
  // sso: the name of the login provider (Authentik …), if one is set up;
  // ssoOnly: the password login is only the main admin's way back in.
  let { setup = false, invite = '', sso = '', ssoOnly = false, onDone } = $props();
  let username = $state('');
  let password = $state('');
  let password2 = $state('');
  let error = $state('');
  // Coming back from a failed login at the provider: /?sso_error=<code>
  const ssoErrors = {
    group: 'Dein Konto beim Anmeldedienst hat keinen Zugang zu Heimdeck.',
    nouser: 'Zu dieser Anmeldung gibt es noch kein Heimdeck-Konto. Bitte den Admin, dich einzuladen – oder melde dich mit deinem Passwort an und verknüpfe das Konto in den Einstellungen.',
    disabled: 'Dieses Konto ist gesperrt.',
    denied: 'Die Anmeldung wurde abgebrochen.',
    linked_other: 'Diese Anmeldung ist schon mit einem anderen Konto verknüpft.',
    state: 'Die Anmeldung ist abgelaufen – bitte noch einmal versuchen.',
    config: 'Die Anmeldung über SSO ist nicht eingerichtet.',
    provider: 'Die Anmeldung über SSO hat nicht geklappt – Details stehen im Protokoll von Heimdeck.',
  };
  {
    const url = new URL(location.href);
    const code = url.searchParams.get('sso_error');
    if (code) {
      error = t(ssoErrors[code] || ssoErrors.provider);
      url.searchParams.delete('sso_error');
      history.replaceState(null, '', url);
    }
  }
  let showSso = $derived(!!sso && !setup && !newAccount);
  let busy = $state(false);
  // svelte-ignore state_referenced_locally
  let inviteOk = $state(invite ? null : false); // null: still checking
  // svelte-ignore state_referenced_locally
  let newAccount = $derived(setup || (invite && inviteOk));

  // svelte-ignore state_referenced_locally
  if (invite) {
    get(`/invite/${encodeURIComponent(invite)}`)
      .then((res) => {
        inviteOk = res.valid;
        if (res.valid && res.language) setLang(res.language);
      })
      .catch(() => (inviteOk = false));
  }

  // Without the invitation in the address, a reload shows the normal login.
  function dropInvite() {
    const url = new URL(location.href);
    url.searchParams.delete('invite');
    history.replaceState(null, '', url);
  }

  async function submit(e) {
    e.preventDefault();
    error = '';
    if (newAccount && password !== password2) {
      error = t('Die Passwörter stimmen nicht überein');
      return;
    }
    busy = true;
    try {
      const path = setup ? '/setup' : newAccount ? `/invite/${encodeURIComponent(invite)}` : '/login';
      await post(path, { username, password, language: i18n.lang });
      if (invite) dropInvite();
      onDone();
    } catch (err) {
      error = err.message;
    } finally {
      busy = false;
    }
  }
</script>

<div class="wrap">
  <form class="card box" onsubmit={submit}>
    <img src="/icon.svg" alt="" width="48" height="48" />
    <div class="head">
      <h1>Heimdeck</h1>
      <div class="langs" role="group" aria-label={t('Sprache')}>
        <button type="button" class:on={i18n.lang === 'de'} onclick={() => setLang('de')}>Deutsch</button>
        <button type="button" class:on={i18n.lang === 'en'} onclick={() => setLang('en')}>English</button>
      </div>
    </div>
    {#if setup}
      <p class="muted">{t('Willkommen! Lege zuerst deinen Zugang an. Damit ist dein Dashboard vor fremden Blicken geschützt.')}</p>
    {:else if invite && inviteOk === null}
      <p class="muted">{t('Einladung wird geprüft …')}</p>
    {:else if invite && inviteOk}
      <p class="muted">{t('Du wurdest zu Heimdeck eingeladen! Wähle deinen Benutzernamen und ein Passwort – danach baust du dir dein eigenes Dashboard.')}</p>
    {:else if invite}
      <p class="bad">{t('Dieser Einladungslink ist ungültig oder abgelaufen')}. {t('Bitte frag nach einem neuen – oder melde dich an, falls du schon einen Zugang hast.')}</p>
    {/if}
    {#if showSso && ssoOnly}
      <a class="button primary sso" href="/api/oidc/login">🔑 {t('Mit {name} anmelden', { name: sso })}</a>
      {#if error}<p class="bad">{error}</p>{/if}
      <details class="fallback">
        <summary>{t('Mit Passwort anmelden (Notzugang für den Hauptadmin)')}</summary>
        {@render fields()}
      </details>
    {:else}
      {@render fields()}
      {#if showSso}
        <div class="or"><span>{t('oder')}</span></div>
        <a class="button sso" href="/api/oidc/login">🔑 {t('Mit {name} anmelden', { name: sso })}</a>
      {/if}
    {/if}
  </form>
  <Credit />
</div>

{#snippet fields()}
  <label class="field">
    <span>{t('Benutzername')}</span>
    <!-- svelte-ignore a11y_autofocus -->
    <input bind:value={username} autocomplete="username" required={!(showSso && ssoOnly)} autofocus={!(showSso && ssoOnly)} />
  </label>
  <label class="field">
    <span>{t('Passwort')}</span>
    <input type="password" bind:value={password} autocomplete={newAccount ? 'new-password' : 'current-password'} required={!(showSso && ssoOnly)} minlength={newAccount ? 8 : undefined} />
  </label>
  {#if newAccount}
    <label class="field">
      <span>{t('Passwort wiederholen')}</span>
      <input type="password" bind:value={password2} autocomplete="new-password" required />
    </label>
  {/if}
  {#if error && !(showSso && ssoOnly)}<p class="bad">{error}</p>{/if}
  <button class="primary" disabled={busy || inviteOk === null}>{newAccount ? t('Zugang anlegen') : t('Anmelden')}</button>
{/snippet}

<style>
  .sso { justify-content: center; text-align: center; text-decoration: none; }
  .or { display: flex; align-items: center; gap: 10px; color: var(--muted); font-size: 12.5px; }
  .or::before, .or::after { content: ''; flex: 1; border-top: 1px solid var(--border); }
  .fallback { display: grid; gap: 12px; font-size: 13px; }
  .fallback[open] { display: grid; }
  .fallback summary { cursor: pointer; color: var(--muted); margin-bottom: 8px; }
  .fallback :global(.field) { margin-bottom: 12px; }
  .wrap { min-height: 100vh; display: grid; place-items: center; align-content: center; padding: 16px; }
  .box { width: min(380px, 100%); padding: 32px; display: grid; gap: 16px; }
  h1 { margin: 0; font-size: 24px; }
  .head { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
  .langs { margin-left: auto; display: flex; border: 1px solid var(--border-strong); border-radius: var(--r-sm); overflow: hidden; }
  .langs button { border: 0; border-radius: 0; padding: 4px 10px; font-size: 12.5px; background: transparent; color: var(--muted); }
  .langs button.on { background: var(--accent); color: var(--on-accent, #fff); }
  p { margin: 0; font-size: 14px; line-height: 1.5; }
  img { border-radius: var(--r-md); }
</style>
