<script>
  import { t } from './i18n.svelte.js';

  // Closes via ✕, the dialog's own Cancel button or Esc – not by clicking next
  // to it, which threw away everything typed when a text selection ended outside.
  // Esc in a text field first only leaves the field (and closes the browser's
  // autocomplete); a second Esc closes the dialog.
  let { title, onClose, children, footer, wide = false } = $props();
  let backdrop;

  function onKey(e) {
    if (e.key !== 'Escape' || e.defaultPrevented) return;
    // Only the topmost dialog (e.g. a confirmation above the tile editor).
    const all = document.querySelectorAll('.backdrop');
    if (all[all.length - 1] !== backdrop) return;
    const el = document.activeElement;
    if (el && backdrop.contains(el) && el.matches('input, textarea, select, [contenteditable]')) {
      el.blur();
      return;
    }
    e.preventDefault();
    onClose();
  }
</script>

<svelte:window onkeydown={onKey} />

<div class="backdrop" role="presentation" bind:this={backdrop}>
  <div class="modal card" class:wide role="dialog" aria-modal="true" aria-label={title}>
    <header>
      <h2>{title}</h2>
      <button class="ghost icon" onclick={onClose} aria-label={t('Schließen')}>✕</button>
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
    /* dvh: the visible height on phones (100vh includes the browser bars) */
    width: min(540px, 100%); max-height: calc(100vh - 32px); max-height: calc(100dvh - 32px);
    display: flex; flex-direction: column; background: var(--surface-solid); overflow: hidden;
  }
  .modal.wide { width: min(780px, 100%); }
  header { display: flex; align-items: center; padding: 16px 16px 8px 20px; }
  h2 { margin: 0; font-size: 18px; flex: 1; }
  /* min-height: 0 lets the body shrink and scroll, so the footer (Save) stays visible */
  .body { padding: 8px 20px 20px; overflow-y: auto; overscroll-behavior: contain; display: grid; gap: 14px; align-content: start; min-height: 0; flex: 1 1 auto; }
  header, footer { flex: none; }
  @media (max-width: 520px) {
    .backdrop { padding: 8px; }
    .modal { max-height: calc(100vh - 16px); max-height: calc(100dvh - 16px); }
    .body { padding: 8px 16px 16px; }
    footer { padding: 12px 16px; }
  }
  footer { padding: 14px 20px; border-top: 1px solid var(--border); display: flex; gap: 8px; align-items: center; flex-wrap: wrap; }
  @keyframes fade { from { opacity: 0; } }
</style>
