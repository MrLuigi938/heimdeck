<script>
  import { post } from './api.js';
  import { t, i18n, setLang } from './i18n.svelte.js';

  let { setup = false, onDone } = $props();
  let username = $state('');
  let password = $state('');
  let password2 = $state('');
  let error = $state('');
  let busy = $state(false);

  async function submit(e) {
    e.preventDefault();
    error = '';
    if (setup && password !== password2) {
      error = t('Die Passwörter stimmen nicht überein');
      return;
    }
    busy = true;
    try {
      await post(setup ? '/setup' : '/login', { username, password, language: i18n.lang });
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
    {/if}
    <label class="field">
      <span>{t('Benutzername')}</span>
      <!-- svelte-ignore a11y_autofocus -->
      <input bind:value={username} autocomplete="username" required autofocus />
    </label>
    <label class="field">
      <span>{t('Passwort')}</span>
      <input type="password" bind:value={password} autocomplete={setup ? 'new-password' : 'current-password'} required minlength={setup ? 8 : undefined} />
    </label>
    {#if setup}
      <label class="field">
        <span>{t('Passwort wiederholen')}</span>
        <input type="password" bind:value={password2} autocomplete="new-password" required />
      </label>
    {/if}
    {#if error}<p class="bad">{error}</p>{/if}
    <button class="primary" disabled={busy}>{setup ? t('Zugang anlegen') : t('Anmelden')}</button>
  </form>
</div>

<style>
  .wrap { min-height: 100vh; display: grid; place-items: center; padding: 16px; }
  .box { width: min(380px, 100%); padding: 32px; display: grid; gap: 16px; }
  h1 { margin: 0; font-size: 24px; }
  .head { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
  .langs { margin-left: auto; display: flex; border: 1px solid var(--border-strong); border-radius: 10px; overflow: hidden; }
  .langs button { border: 0; border-radius: 0; padding: 4px 10px; font-size: 12.5px; background: transparent; color: var(--muted); }
  .langs button.on { background: var(--accent); color: #fff; }
  p { margin: 0; font-size: 14px; line-height: 1.5; }
  img { border-radius: 12px; }
</style>
