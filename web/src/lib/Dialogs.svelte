<script>
  import { dialogs, close } from './dialog.svelte.js';
  import { t } from './i18n.svelte.js';
  import Modal from './Modal.svelte';

  function submit(e, d) {
    e.preventDefault();
    if (d.kind === 'prompt') {
      if (!d.value.trim()) return;
      close(d, d.value.trim());
    } else {
      close(d, true);
    }
  }
  const cancel = (d) => close(d, d.kind === 'prompt' ? null : false);
</script>

{#each dialogs as d (d.id)}
  <Modal title={d.title || (d.kind === 'prompt' ? t('Eingabe') : t('Bitte bestätigen'))} onClose={() => cancel(d)}>
    <form id="dialog-{d.id}" class="form" onsubmit={(e) => submit(e, d)}>
      {#if d.message}<p>{d.message}</p>{/if}
      {#if d.kind === 'prompt'}
        <!-- svelte-ignore a11y_autofocus -->
        <input bind:value={d.value} placeholder={d.placeholder} autofocus />
      {/if}
    </form>
    {#snippet footer()}
      <span class="spacer"></span>
      <button onclick={() => cancel(d)}>{t('Abbrechen')}</button>
      <!-- svelte-ignore a11y_autofocus -->
      <button class={d.danger ? 'danger-fill' : 'primary'} type="submit" form="dialog-{d.id}" autofocus={d.kind === 'confirm'}>
        {d.confirmLabel || (d.kind === 'prompt' ? t('OK') : d.danger ? t('Löschen') : t('OK'))}
      </button>
    {/snippet}
  </Modal>
{/each}

<style>
  .form { display: grid; gap: 12px; }
  p { margin: 0; line-height: 1.5; }
  .danger-fill { background: var(--bad); border-color: var(--bad); color: #fff; }
</style>
