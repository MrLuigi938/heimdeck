<script>
  import { get } from './lib/api.js';
  import Login from './lib/Login.svelte';
  import Dashboard from './lib/Dashboard.svelte';
  import Toasts from './lib/Toasts.svelte';

  let state = $state(null);

  async function load() {
    try {
      state = await get('/state');
    } catch (e) {
      state = { error: e.message };
    }
  }

  load();
  window.addEventListener('heimdeck:logout', load);
</script>

{#if !state}
  <div class="center muted">Lade …</div>
{:else if state.error}
  <div class="center bad">Server nicht erreichbar: {state.error}</div>
{:else if state.setup || !state.authenticated}
  <Login setup={state.setup} onDone={load} />
{:else}
  <Dashboard />
{/if}
<Toasts />

<style>
  .center { min-height: 100vh; display: grid; place-items: center; }
</style>
