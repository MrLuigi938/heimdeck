<script>
  import { t } from './i18n.svelte.js';

  // The quiet signature under the login box and at the bottom of every dashboard.
  // On the dashboard it also names the version and, when the server found one,
  // links a newer release – to its notes on the project website.
  let { app = false, version = '', update = null } = $props();
  let shownVersion = $derived(version && version !== 'dev' ? version : '');
</script>

<footer class="credit">
  <span class="on-bg">
    {#if app}<b>Heimdeck</b>{#if shownVersion}<span class="version">{shownVersion}</span>{/if}{' · '}{/if}{t('Erdacht mit')} <span class="heart" aria-label={t('Liebe')}>♥</span> {t('von')} <a href="https://github.com/MrLuigi938" target="_blank" rel="noopener noreferrer">Luis Blum</a> · {t('gebaut mit KI')} ✨
  </span>
  {#if app && update}
    <a class="update on-bg" href={update.site || update.url} target="_blank" rel="noopener noreferrer" title={t('Was ist neu in {v}?', { v: update.version })}>
      ⬆ {t('Version {v} ist da', { v: update.version })}
    </a>
  {/if}
</footer>

<style>
  .credit { display: flex; flex-wrap: wrap; justify-content: center; align-items: center; gap: 8px; padding: 28px 16px 20px; font-size: 14px; color: var(--muted); text-align: center; }
  .credit > span { padding: 4px 12px; border-radius: 999px; }
  b { font-weight: 600; color: var(--text); }
  .version { margin-left: 6px; font-variant-numeric: tabular-nums; }
  .heart { color: #e5484d; }
  a { color: inherit; font-weight: 600; text-decoration: none; border-bottom: 1px dotted currentColor; }
  a:hover { color: var(--accent); border-bottom-color: var(--accent); }
  .update {
    border: 1px solid color-mix(in srgb, var(--accent) 45%, transparent); border-radius: 999px; padding: 3px 12px;
    color: var(--accent); background: color-mix(in srgb, var(--accent) 10%, transparent); font-size: 13px;
  }
  .update:hover { border-color: var(--accent); }
</style>
