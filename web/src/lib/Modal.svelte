<script>
  let { title, onClose, children, footer, wide = false } = $props();

  function onKey(e) {
    if (e.key === 'Escape') onClose();
  }
</script>

<svelte:window onkeydown={onKey} />

<div class="backdrop" onclick={(e) => e.target === e.currentTarget && onClose()} role="presentation">
  <div class="modal card" class:wide role="dialog" aria-modal="true" aria-label={title}>
    <header>
      <h2>{title}</h2>
      <button class="ghost icon" onclick={onClose} aria-label="Schließen">✕</button>
    </header>
    <div class="body">{@render children()}</div>
    {#if footer}<footer>{@render footer()}</footer>{/if}
  </div>
</div>

<style>
  .backdrop {
    position: fixed; inset: 0; z-index: 50; background: rgba(0, 0, 0, .55);
    display: grid; place-items: center; padding: 16px; animation: fade .12s;
  }
  .modal {
    width: min(540px, 100%); max-height: calc(100vh - 32px); display: flex; flex-direction: column;
    background: var(--surface-solid);
  }
  .modal.wide { width: min(780px, 100%); }
  header { display: flex; align-items: center; padding: 16px 16px 8px 20px; }
  h2 { margin: 0; font-size: 18px; flex: 1; }
  .body { padding: 8px 20px 20px; overflow-y: auto; display: grid; gap: 14px; }
  footer { padding: 14px 20px; border-top: 1px solid var(--border); display: flex; gap: 8px; align-items: center; flex-wrap: wrap; }
  @keyframes fade { from { opacity: 0; } }
</style>
