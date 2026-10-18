<script>
  import { post } from './api.js';

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
      error = 'Die Passwörter stimmen nicht überein';
      return;
    }
    busy = true;
    try {
      await post(setup ? '/setup' : '/login', { username, password });
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
    <h1>Heimdeck</h1>
    {#if setup}
      <p class="muted">Willkommen! Lege zuerst deinen Zugang an. Damit ist dein Dashboard vor fremden Blicken geschützt.</p>
    {/if}
    <label class="field">
      <span>Benutzername</span>
      <!-- svelte-ignore a11y_autofocus -->
      <input bind:value={username} autocomplete="username" required autofocus />
    </label>
    <label class="field">
      <span>Passwort</span>
      <input type="password" bind:value={password} autocomplete={setup ? 'new-password' : 'current-password'} required minlength={setup ? 8 : undefined} />
    </label>
    {#if setup}
      <label class="field">
        <span>Passwort wiederholen</span>
        <input type="password" bind:value={password2} autocomplete="new-password" required />
      </label>
    {/if}
    {#if error}<p class="bad">{error}</p>{/if}
    <button class="primary" disabled={busy}>{setup ? 'Zugang anlegen' : 'Anmelden'}</button>
  </form>
</div>

<style>
  .wrap { min-height: 100vh; display: grid; place-items: center; padding: 16px; }
  .box { width: min(380px, 100%); padding: 32px; display: grid; gap: 16px; }
  h1 { margin: 0; font-size: 24px; }
  p { margin: 0; font-size: 14px; line-height: 1.5; }
  img { border-radius: 12px; }
</style>
