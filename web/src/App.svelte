<script>
  import { get } from './lib/api.js';
  import Login from './lib/Login.svelte';
  import Dashboard from './lib/Dashboard.svelte';
  import Toasts from './lib/Toasts.svelte';
  import Dialogs from './lib/Dialogs.svelte';
  import { t, setLang } from './lib/i18n.svelte.js';
  import { toast } from './lib/toast.svelte.js';

  // Back from linking the account with the login provider: /?sso=linked
  {
    const url = new URL(location.href);
    if (url.searchParams.get('sso') === 'linked') {
      url.searchParams.delete('sso');
      history.replaceState(null, '', url);
      setTimeout(() => toast(t('Dein Konto ist jetzt mit der SSO-Anmeldung verknüpft.')), 500);
    }
  }

  let state = $state(null);
  // An invitation link: /?invite=<token>
  const invite = new URLSearchParams(location.search).get('invite') || '';

  async function load() {
    try {
      state = await get('/state');
      // The saved setting wins over the browser language.
      if (state.language) setLang(state.language);
    } catch (e) {
      state = { error: e.message };
    }
  }

  load();
  window.addEventListener('heimdeck:logout', load);
</script>

{#if !state}
  <div class="center muted">{t('Lade …')}</div>
{:else if state.error}
  <div class="center bad">{t('Server nicht erreichbar')}: {state.error}</div>
{:else if state.setup || !state.authenticated}
  <Login setup={state.setup} invite={state.setup ? '' : invite} sso={state.sso || ''} ssoOnly={!!state.ssoOnly} onDone={load} />
{:else}
  <Dashboard />
{/if}
<Dialogs />
<Toasts />

<style>
  .center { min-height: 100vh; display: grid; place-items: center; }
</style>
