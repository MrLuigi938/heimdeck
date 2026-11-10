<script>
  // The simple view: the workflow as one sentence of chips – "When … and only
  // if … then … → …". A tap on a chip opens it right below the sentence;
  // the dashed chips add a part. Same data as the flow and the list.
  import { t } from './i18n.svelte.js';
  import { blankTrigger, blankCond, blankStep, triggerText, triggerIcon, condText, stepText } from './workflow.js';
  import WfTrigger from './WfTrigger.svelte';
  import WfCond from './WfCond.svelte';
  import WfStep from './WfStep.svelte';

  let { wf = $bindable(), tiles = [], scenes = [], ai = null } = $props();

  // The chip being edited: { kind: 'trigger' | 'cond' | 'step', i, branch?, j? }
  let sel = $state(null);
  const same = (a, b) => a && b && a.kind === b.kind && a.i === b.i && a.branch === b.branch && a.j === b.j;
  const pick = (s) => (sel = same(sel, s) ? null : s);

  function addTrigger() {
    wf.triggers.push(blankTrigger());
    sel = { kind: 'trigger', i: wf.triggers.length - 1 };
  }
  function addCond() {
    wf.conds.push(blankCond());
    sel = { kind: 'cond', i: wf.conds.length - 1 };
  }
  function addStep(i, branch) {
    if (branch) {
      wf.steps[i][branch].push(blankStep());
      sel = { kind: 'step', i, branch, j: wf.steps[i][branch].length - 1 };
    } else {
      wf.steps.push(blankStep());
      sel = { kind: 'step', i: wf.steps.length - 1 };
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
  function move(d) {
    if (sel?.kind !== 'step') return;
    const list = sel.branch ? wf.steps[sel.i][sel.branch] : wf.steps;
    const at = sel.branch ? sel.j : sel.i;
    const to = at + d;
    if (to < 0 || to >= list.length) return;
    const [x] = list.splice(at, 1);
    list.splice(to, 0, x);
    sel = sel.branch ? { ...sel, j: to } : { ...sel, i: to };
  }

  // A step's colour: what it does.
  const tone = (s) => (s.kind === 'wait' ? 'neutral' : s.kind === 'if' ? 'cond' : 'act');
  // A message with its building blocks as labels.
  const parts = (text) => (text || '').split(/(\{[^{}\n]{1,120}\})/).filter(Boolean);
  let ifCount = $derived(wf.steps.filter((s) => s.kind === 'if').length);
  const title = (s) => (s?.kind === 'trigger' ? t('Auslöser') : s?.kind === 'cond' ? t('Bedingung') : t('Schritt'));
</script>

{#snippet stepChip(s, at)}
  <button class="chip {tone(s)}" class:sel={same(sel, at)} onclick={() => pick(at)}>
    {#if s.kind === 'notify' && s.text}
      ✉ <span class="msg">„{#each parts(s.text) as p, k (k)}{#if p.startsWith('{')}<span class="blk">{p.slice(1, -1)}</span>{:else}{p}{/if}{/each}“</span>{#if s.report} + 📋{/if}{#if s.camera} + 📹{/if}
    {:else if s.kind === 'if'}
      ❓ {t('wenn')} {condText(s.if || {}, tiles)}
    {:else}
      {stepText(s, tiles, scenes)}
    {/if}
  </button>
{/snippet}

{#snippet editor()}
  <div class="pop">
    <div class="pop-head">
      <strong>{title(sel)}</strong>
      <span class="grow"></span>
      {#if sel.kind === 'step'}
        <button class="small ghost" onclick={() => move(-1)} aria-label={t('Nach vorn')} title={t('Nach vorn')}>←</button>
        <button class="small ghost" onclick={() => move(1)} aria-label={t('Nach hinten')} title={t('Nach hinten')}>→</button>
      {/if}
      {#if !(sel.kind === 'trigger' && wf.triggers.length < 2)}<button class="small" onclick={remove}>{t('Entfernen')}</button>{/if}
      <button class="small primary" onclick={() => (sel = null)}>{t('Fertig')}</button>
    </div>
    {#if sel.kind === 'trigger' && wf.triggers[sel.i]}
      <WfTrigger bind:tr={wf.triggers[sel.i]} {tiles} {ai} />
    {:else if sel.kind === 'cond' && wf.conds[sel.i]}
      <WfCond bind:cond={wf.conds[sel.i]} {tiles} />
    {:else if sel.kind === 'step' && sel.branch && wf.steps[sel.i]?.[sel.branch]?.[sel.j]}
      <WfStep bind:step={wf.steps[sel.i][sel.branch][sel.j]} {tiles} {scenes} {ai} nested />
    {:else if sel.kind === 'step' && !sel.branch && wf.steps[sel.i]}
      <WfStep bind:step={wf.steps[sel.i]} {tiles} {scenes} {ai} />
    {/if}
  </div>
{/snippet}

<div class="sentence">
  <div class="line">
    <span class="w">{t('Wenn')}</span>
    {#each wf.triggers as tr, i (i)}
      {#if i > 0}<button class="joiner" onclick={() => (wf.all = !wf.all)} title={t('Zwischen „oder“ und „und“ wechseln')}>{wf.all ? t('und') : t('oder')}</button>{/if}
      <button class="chip trig" class:sel={same(sel, { kind: 'trigger', i })} onclick={() => pick({ kind: 'trigger', i })}>{triggerIcon(tr.kind)} {triggerText(tr, tiles)}</button>
    {/each}
    {#if wf.triggers.length < 5}<button class="chip add" onclick={addTrigger}>{wf.all ? t('und …') : t('oder …')}</button>{/if}
  </div>
  {#if wf.triggers.length > 1}
    <p class="hint">{wf.all ? t('„und“: startet, wenn einer davon eintritt und die anderen in dem Moment auch zutreffen. Tipp auf „und“, um zu „oder“ zu wechseln.') : t('„oder“: einer davon reicht. Tipp auf „oder“, um zu „und“ zu wechseln.')}</p>
  {/if}
  {#if sel?.kind === 'trigger'}{@render editor()}{/if}

  <div class="line">
    {#if wf.conds.length}
      <span class="w">{t('und nur wenn')}</span>
      {#each wf.conds as c, i (i)}
        {#if i > 0}<span class="w">{t('und')}</span>{/if}
        <button class="chip cond" class:sel={same(sel, { kind: 'cond', i })} onclick={() => pick({ kind: 'cond', i })}>✔ {condText(c, tiles)}</button>
      {/each}
      {#if wf.conds.length < 5}<button class="chip add" onclick={addCond}>{t('und …')}</button>{/if}
    {:else}
      <button class="chip add" onclick={addCond}>＋ {t('nur wenn …')}</button>
    {/if}
  </div>
  {#if sel?.kind === 'cond'}{@render editor()}{/if}

  <div class="line">
    <span class="w">{t('dann')}</span>
    {#each wf.steps as s, i (i)}
      {#if i > 0}<span class="arrow">→</span>{/if}
      {@render stepChip(s, { kind: 'step', i })}
      {#if s.kind === 'if'}
        <span class="branch">
          <span class="w">{t('dann')}</span>
          {#each s.then as x, j (j)}{#if j > 0}<span class="arrow">→</span>{/if}{@render stepChip(x, { kind: 'step', i, branch: 'then', j })}{/each}
          <button class="chip add" onclick={() => addStep(i, 'then')} aria-label={t('Schritt einfügen')}>＋</button>
          <span class="w">{t('sonst')}</span>
          {#each s.else as x, j (j)}{#if j > 0}<span class="arrow">→</span>{/if}{@render stepChip(x, { kind: 'step', i, branch: 'else', j })}{/each}
          <button class="chip add" onclick={() => addStep(i, 'else')} aria-label={t('Schritt einfügen')}>＋</button>
        </span>
      {/if}
    {/each}
    <button class="chip add" onclick={() => addStep()}>＋ {t('Schritt')}</button>
  </div>
  {#if sel?.kind === 'step'}{@render editor()}{/if}
  {#if ifCount > 1}<p class="hint">{t('Mit mehreren „Wenn … dann … sonst“ ist der erweiterte Modus übersichtlicher.')}</p>{/if}
</div>

<style>
  .sentence { display: grid; gap: 10px; padding: 14px; border: 1px solid var(--border); border-radius: var(--r-sm); background: var(--surface-2); }
  .line { display: flex; flex-wrap: wrap; align-items: center; gap: 6px; font-size: 15px; line-height: 1.5; }
  .w { color: var(--muted); }
  .arrow { color: var(--muted); }
  .joiner { padding: 2px 10px; border-radius: 999px; font-size: 13px; font-weight: 600; color: var(--accent-text, var(--accent)); background: none; border: 1px solid color-mix(in srgb, var(--accent) 45%, transparent); }
  .joiner:hover { background: color-mix(in srgb, var(--accent) 14%, transparent); }
  .chip {
    --hue: var(--muted);
    display: inline-flex; align-items: center; gap: 4px; max-width: 100%; white-space: normal; text-align: left;
    padding: 4px 11px; border-radius: 999px; font-size: 14px; line-height: 1.35;
    background: color-mix(in srgb, var(--hue) 16%, var(--surface-2));
    border: 1px solid color-mix(in srgb, var(--hue) 55%, transparent); color: var(--text);
  }
  .chip:hover { background: color-mix(in srgb, var(--hue) 26%, var(--surface-2)); }
  .chip.trig { --hue: #7f77dd; }
  .chip.cond { --hue: #e0a12a; }
  .chip.act { --hue: #1d9e75; }
  .chip.neutral { --hue: #888780; }
  .chip.sel { box-shadow: 0 0 0 2px color-mix(in srgb, var(--hue) 70%, transparent); }
  .chip.add { background: none; border: 1px dashed var(--border); color: var(--muted); font-size: 13px; }
  .chip.add:hover { color: var(--text); border-color: var(--accent); }
  .msg { overflow-wrap: anywhere; }
  .blk { display: inline-block; margin: 0 2px; padding: 0 6px; border-radius: 5px; font-size: 12px; background: color-mix(in srgb, var(--accent) 22%, transparent); color: var(--accent-text, var(--text)); }
  .branch { display: inline-flex; flex-wrap: wrap; align-items: center; gap: 6px; padding: 4px 8px; border-left: 2px solid #e0a12a; }
  .pop { display: grid; gap: 8px; padding: 12px; border: 1px solid var(--accent); border-radius: var(--r-sm); background: var(--surface); }
  .pop-head { display: flex; gap: 6px; align-items: center; }
  .grow { flex: 1; }
  .hint { margin: 0; font-size: 12px; color: var(--muted); }
</style>
