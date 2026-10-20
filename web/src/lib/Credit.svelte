<script>
  import { t } from './i18n.svelte.js';

  // The quiet signature under the login box and at the bottom of every dashboard.
  let { app = false } = $props();
</script>

<footer class="credit">
  <span class="on-bg">
    {#if app}<b>Heimdeck</b>{' · '}{/if}{t('Erdacht mit')} <span class="heart" aria-label={t('Liebe')}>♥</span> {t('von')} <a href="https://github.com/MrLuigi938" target="_blank" rel="noopener noreferrer">Luis Blum</a> · {t('gebaut mit KI')} ✨
  </span>
</footer>

<style>
  .credit { display: flex; justify-content: center; padding: 28px 16px 20px; font-size: 12px; color: var(--muted); text-align: center; }
  .credit > span { padding: 4px 12px; border-radius: 999px; }
  b { font-weight: 600; color: var(--text); }
  .heart { color: #e5484d; }
  a { color: inherit; font-weight: 600; text-decoration: none; border-bottom: 1px dotted currentColor; }
  a:hover { color: var(--accent); border-bottom-color: var(--accent); }
</style>
