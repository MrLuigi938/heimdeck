<script>
  // The flow view: the workflow drawn from top to bottom – triggers, conditions,
  // steps; an "if" splits into "then" and "else". A tap on a block opens it
  // below the drawing, "+" adds a block in that place.
  import { t } from './i18n.svelte.js';
  import { blankTrigger, blankCond, blankStep, triggerText, triggerIcon, condText, stepText } from './workflow.js';
  import WfTrigger from './WfTrigger.svelte';
  import WfCond from './WfCond.svelte';
  import WfStep from './WfStep.svelte';

  let { wf = $bindable(), tiles = [], scenes = [], ai = null } = $props();

  // The block being edited: { kind: 'trigger' | 'cond' | 'step', i, branch?, j? }
  let sel = $state(null);
  const same = (a, b) => a && b && a.kind === b.kind && a.i === b.i && a.branch === b.branch && a.j === b.j;

  function addTrigger() {
    wf.triggers.push(blankTrigger());
    sel = { kind: 'trigger', i: wf.triggers.length - 1 };
  }
  function addCond() {
    wf.conds.push(blankCond());
    sel = { kind: 'cond', i: wf.conds.length - 1 };
  }
  function insertStep(i, branch, j) {
    if (branch) {
      wf.steps[i][branch].splice(j, 0, blankStep());
      sel = { kind: 'step', i, branch, j };
    } else {
      wf.steps.splice(i, 0, blankStep());
      sel = { kind: 'step', i };
    }
  }
  function remove() {
    if (!sel) return;
    if (sel.kind === 'trigger') {
      if (wf.triggers.length > 1) wf.triggers.splice(sel.i, 1);
    } else if (sel.kind === 'cond') wf.conds.splice(sel.i, 1);
    else if (sel.branch) wf.steps[sel.i][sel.branch].splice(sel.j, 1);
    else wf.steps.splice(sel.i, 1);
    sel = null;
  }
  const title = (s) => (s?.kind === 'trigger' ? t('Auslöser') : s?.kind === 'cond' ? t('Bedingung') : t('Schritt'));
</script>

<div class="wf-flow">
  <span class="lane">{t('Wenn …')}</span>
  <div class="trow">
    {#each wf.triggers as tr, i (i)}
      {#if i > 0}<span class="or">{t('oder')}</span>{/if}
      <button class="node trig" class:sel={same(sel, { kind: 'trigger', i })} onclick={() => (sel = { kind: 'trigger', i })}>{triggerIcon(tr.kind)} {triggerText(tr, tiles)}</button>
    {/each}
    {#if wf.triggers.length < 5}<button class="add" onclick={addTrigger} title={t('Auslöser hinzufügen')} aria-label={t('Auslöser hinzufügen')}>＋</button>{/if}
  </div>
  <div class="edge"></div>
  {#each wf.conds as c, i (i)}
    <button class="node cond" class:sel={same(sel, { kind: 'cond', i })} onclick={() => (sel = { kind: 'cond', i })}>✔ {condText(c, tiles)}</button>
    <div class="edge"></div>
  {/each}
  {#if wf.conds.length < 5}
    <button class="add wide" onclick={addCond}>＋ {t('Nur wenn …')}</button>
    <div class="edge"></div>
  {/if}
  <span class="lane">{t('… dann')}</span>
  {#each wf.steps as s, i (i)}
    <button class="add" onclick={() => insertStep(i)} title={t('Schritt einfügen')} aria-label={t('Schritt einfügen')}>＋</button>
    <div class="edge"></div>
    <button class="node step" class:ifnode={s.kind === 'if'} class:sel={same(sel, { kind: 'step', i })} onclick={() => (sel = { kind: 'step', i })}>{stepText(s, tiles, scenes)}</button>
    {#if s.kind === 'if'}
      <div class="branches">
        {#each ['then', 'else'] as branch (branch)}
          <div class="branch">
            <span class="blabel">{branch === 'then' ? t('dann') : t('sonst')}</span>
            {#each s[branch] as x, j (j)}
              <div class="edge"></div>
              <button class="node step" class:sel={same(sel, { kind: 'step', i, branch, j })} onclick={() => (sel = { kind: 'step', i, branch, j })}>{stepText(x, tiles, scenes)}</button>
            {/each}
            <div class="edge"></div>
            <button class="add" onclick={() => insertStep(i, branch, s[branch].length)} title={t('Schritt einfügen')} aria-label={t('Schritt einfügen')}>＋</button>
          </div>
        {/each}
      </div>
    {/if}
    <div class="edge"></div>
  {/each}
  <button class="add" onclick={() => insertStep(wf.steps.length)} title={t('Schritt einfügen')} aria-label={t('Schritt einfügen')}>＋</button>
  <div class="edge"></div>
  <span class="end">{t('Ende')}</span>
</div>

{#if sel}
  <div class="panel">
    <div class="panel-head">
      <strong>{title(sel)}</strong>
      <span class="grow"></span>
      {#if !(sel.kind === 'trigger' && wf.triggers.length < 2)}
        <button class="small" onclick={remove}>{t('Entfernen')}</button>
      {/if}
      <button class="small ghost" onclick={() => (sel = null)} aria-label={t('Schließen')} title={t('Schließen')}>✕</button>
    </div>
    {#if sel.kind === 'trigger' && wf.triggers[sel.i]}
      <WfTrigger bind:tr={wf.triggers[sel.i]} {tiles} />
    {:else if sel.kind === 'cond' && wf.conds[sel.i]}
      <WfCond bind:cond={wf.conds[sel.i]} {tiles} />
    {:else if sel.kind === 'step' && sel.branch && wf.steps[sel.i]?.[sel.branch]?.[sel.j]}
      <WfStep bind:step={wf.steps[sel.i][sel.branch][sel.j]} {tiles} {scenes} {ai} nested />
    {:else if sel.kind === 'step' && !sel.branch && wf.steps[sel.i]}
      <WfStep bind:step={wf.steps[sel.i]} {tiles} {scenes} {ai} />
    {/if}
  </div>
{/if}

<style>
  .wf-flow { display: flex; flex-direction: column; align-items: center; padding: 12px 4px; border: 1px solid var(--border); border-radius: var(--r-sm); background: var(--surface-2); overflow-x: auto; }
  .lane { font-size: 11px; text-transform: uppercase; letter-spacing: .06em; color: var(--muted); margin: 2px 0 6px; }
  .trow { display: flex; flex-wrap: wrap; gap: 6px; align-items: center; justify-content: center; }
  .or { font-size: 12px; color: var(--muted); }
  .node { max-width: min(320px, 100%); min-width: 160px; justify-content: center; text-align: center; white-space: normal; padding: 8px 12px; border: 1px solid var(--border); border-radius: var(--r-sm); background: var(--surface); font-size: 13px; line-height: 1.35; }
  .node.trig { border-color: color-mix(in srgb, var(--accent) 45%, var(--border)); }
  .node.cond { border-style: dashed; }
  .node.ifnode { border-color: var(--warn, #d9a400); }
  .node.sel { border-color: var(--accent); box-shadow: 0 0 0 2px color-mix(in srgb, var(--accent) 35%, transparent); }
  .edge { width: 2px; height: 14px; background: var(--border); flex: none; }
  .add { width: 26px; height: 26px; padding: 0; justify-content: center; border-radius: 50%; font-size: 13px; flex: none; }
  .add.wide { width: auto; border-radius: 13px; padding: 0 10px; font-size: 12px; }
  .branches { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 10px; width: 100%; max-width: 680px; margin-top: 8px; padding-top: 8px; border-top: 2px solid var(--border); }
  .branch { display: flex; flex-direction: column; align-items: center; min-width: 0; }
  .branch .node { min-width: 0; width: 100%; }
  .blabel { font-size: 11px; text-transform: uppercase; letter-spacing: .06em; color: var(--muted); }
  .end { font-size: 12px; color: var(--muted); }
  .panel { display: grid; gap: 8px; margin-top: 10px; padding: 12px; border: 1px solid var(--accent); border-radius: var(--r-sm); }
  .panel-head { display: flex; gap: 6px; align-items: center; }
  .grow { flex: 1; }
</style>
