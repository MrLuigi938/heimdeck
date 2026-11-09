<script>
  // The steps of a workflow as a list of blocks (list view); an "if" holds two
  // smaller lists, for "then" and "else".
  import { t } from './i18n.svelte.js';
  import { blankStep } from './workflow.js';
  import WfStep from './WfStep.svelte';
  import WfSteps from './WfSteps.svelte';

  let { steps = $bindable(), tiles = [], scenes = [], ai = null, nested = false } = $props();
  function move(i, d) {
    const j = i + d;
    if (j < 0 || j >= steps.length) return;
    [steps[i], steps[j]] = [steps[j], steps[i]];
  }
</script>

<div class="wf-steps" class:nested>
  {#each steps as s, i (i)}
    <div class="wf-block" class:if={s.kind === 'if'}>
      <div class="wf-block-head">
        <span class="wf-n">{i + 1}</span>
        <span class="grow"></span>
        <button class="small ghost" onclick={() => move(i, -1)} disabled={i === 0} aria-label={t('Nach oben')} title={t('Nach oben')}>↑</button>
        <button class="small ghost" onclick={() => move(i, 1)} disabled={i === steps.length - 1} aria-label={t('Nach unten')} title={t('Nach unten')}>↓</button>
        <button class="small ghost" onclick={() => steps.splice(i, 1)} aria-label={t('Entfernen')} title={t('Entfernen')}>✕</button>
      </div>
      <WfStep bind:step={steps[i]} {tiles} {scenes} {ai} {nested} />
      {#if s.kind === 'if'}
        <div class="wf-branches">
          <div class="wf-branch">
            <span class="wf-blabel">{t('dann')}</span>
            <WfSteps bind:steps={steps[i].then} {tiles} {scenes} {ai} nested />
          </div>
          <div class="wf-branch">
            <span class="wf-blabel">{t('sonst')}</span>
            <WfSteps bind:steps={steps[i].else} {tiles} {scenes} {ai} nested />
          </div>
        </div>
      {/if}
    </div>
  {/each}
  <button class="small" onclick={() => steps.push(blankStep())}>＋ {t('Schritt')}</button>
</div>
