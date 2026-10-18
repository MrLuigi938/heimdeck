<script>
  import { get } from './lib/api.js';
  import Login from './lib/Login.svelte';
  import Dashboard from './lib/Dashboard.svelte';
  import Toasts from './lib/Toasts.svelte';
  import { t, setLang } from './lib/i18n.svelte.js';

  let state = $state(null);

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
  <Login setup={state.setup} onDone={load} />
{:else}
  <Dashboard />
{/if}
<Toasts />

<style>
  .center { min-height: 100vh; display: grid; place-items: center; }
</style>
