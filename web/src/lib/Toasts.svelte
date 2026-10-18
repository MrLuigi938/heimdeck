<script>
  import { toasts } from './toast.svelte.js';
</script>

<div class="toasts" aria-live="polite">
  {#each toasts as t (t.id)}
    <div class="toast card {t.tone}">{t.text}</div>
  {/each}
</div>

<style>
  .toasts {
    position: fixed; bottom: 20px; left: 50%; transform: translateX(-50%);
    display: grid; gap: 8px; z-index: 100; width: min(420px, calc(100vw - 32px));
  }
  .toast { padding: 12px 16px; border-left: 4px solid var(--ok); background: var(--surface-solid); }
  .toast.bad { border-left-color: var(--bad); }
  .toast.warn { border-left-color: var(--warn); }
</style>
