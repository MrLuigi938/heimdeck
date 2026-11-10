<script>
  // One step of a workflow. An "if" shows its condition here; its two lists of
  // steps are drawn by the list or flow view.
  import { t, i18n } from './i18n.svelte.js';
  import { blankStep } from './workflow.js';
  import WfCond from './WfCond.svelte';

  let { step = $bindable(), tiles = [], scenes = [], ai = null, nested = false } = $props();
  const tileById = (id) => tiles.find((x) => x.id === Number(id));
  let withButtons = $derived(tiles.filter((x) => x.actions.length));
  let cameras = $derived(tiles.filter((x) => x.kind === 'camera'));
  const sameParams = (a, b) => JSON.stringify(a || {}) === JSON.stringify(b || {});
  let buttonIndex = $derived((tileById(step.tile)?.actions || []).findIndex((x) => x.id === step.action && sameParams(x.params, step.params)));
  function pickButton(i) {
    const b = tileById(step.tile)?.actions[i];
    step.action = b?.id || '';
    step.params = b?.params || null;
    step.confirm = b?.confirm || '';
  }
  // Building blocks for the message: filled in with the current values when it goes out.
  let textEl = $state();
  let blocks = $derived(
    tiles
      .map((x) => ({
        name: x.name,
        items: [
          ...x.stats.map((st) => ({ code: `{${x.name} · ${st.label}}`, label: `${st.label} (${t('jetzt')}: ${st.value})` })),
          ...(x.list ? [{ code: `{${x.name} · ${t('Liste')}}`, label: `${t('Liste')}: ${x.list}` }] : []),
          ...(x.monitored ? [{ code: `{${x.name} · Status}`, label: t('Status (erreichbar oder nicht)') }] : []),
        ],
      }))
      .filter((g) => g.items.length),
  );
  function insertBlock(code) {
    if (!code) return;
    const text = step.text || '';
    const at = textEl && document.activeElement !== null && textEl.selectionStart != null ? textEl.selectionStart : text.length;
    const end = textEl?.selectionEnd ?? at;
    step.text = text.slice(0, at) + code + text.slice(end);
    queueMicrotask(() => {
      textEl?.focus();
      textEl?.setSelectionRange(at + code.length, at + code.length);
    });
  }
  // Changing the kind keeps nothing of the old step.
  function changeKind(kind) {
    const fresh = blankStep(kind);
    for (const k of Object.keys(step)) delete step[k];
    Object.assign(step, fresh);
  }
</script>

<div class="wf-fields">
  <select value={step.kind} onchange={(e) => changeKind(e.currentTarget.value)} aria-label={t('Schritt')}>
    <option value="notify">✉ {t('Nachricht schicken')}</option>
    <option value="button">▶ {t('Knopf einer Kachel drücken')}</option>
    <option value="scene">🎬 {t('Eine Szene starten')}</option>
    <option value="wait">⏳ {t('Warten')}</option>
    {#if !nested}<option value="if">❓ {t('Wenn … dann … sonst')}</option>{/if}
  </select>
  {#if step.kind === 'notify'}
    <textarea bind:this={textEl} bind:value={step.text} rows="2" maxlength="1000" placeholder={t('Der Text der Nachricht – sie geht an deine Benachrichtigungs-Kanäle.')}></textarea>
    <select value="" onchange={(e) => { insertBlock(e.currentTarget.value); e.currentTarget.value = ''; }} aria-label={t('Baustein einfügen')} class="wf-blocks">
      <option value="">＋ {t('Baustein einfügen – wird beim Senden mit dem aktuellen Wert gefüllt')}</option>
      <optgroup label={t('Allgemein')}>
        <option value={i18n.lang === 'en' ? '{reason}' : '{Grund}'}>{t('Grund – warum der Workflow startet')}</option>
        <option value={i18n.lang === 'en' ? '{time}' : '{Uhrzeit}'}>{t('Uhrzeit')}</option>
        <option value={i18n.lang === 'en' ? '{date}' : '{Datum}'}>{t('Datum')}</option>
      </optgroup>
      {#each blocks as g (g.name)}
        <optgroup label={g.name}>
          {#each g.items as b (b.code)}<option value={b.code}>{b.label}</option>{/each}
        </optgroup>
      {/each}
    </select>
    {#if ai?.dashboard}<label class="check"><input type="checkbox" bind:checked={step.report} /> 📋 {t('Lagebericht der KI anhängen')}</label>{/if}
    {#if cameras.length}
      <select bind:value={step.camera} aria-label={t('Kamerabild')}>
        <option value={0}>{t('ohne Kamerabild')}</option>
        {#each cameras as x (x.id)}<option value={x.id}>📹 {t('Bild von {name} mitschicken (Telegram)', { name: x.name })}</option>{/each}
      </select>
    {/if}
  {:else if step.kind === 'button'}
    <div class="wf-row">
      <select bind:value={step.tile} onchange={() => pickButton(-1)} aria-label={t('Kachel')}>
        <option value={0}>{t('Kachel wählen …')}</option>
        {#each withButtons as x (x.id)}<option value={x.id}>{x.name} ({x.group})</option>{/each}
      </select>
      <select value={buttonIndex} onchange={(e) => pickButton(Number(e.currentTarget.value))} disabled={!step.tile} aria-label={t('Knopf')}>
        <option value={-1}>{t('Knopf wählen …')}</option>
        {#each tileById(step.tile)?.actions || [] as b, j (j)}<option value={j}>{b.label}</option>{/each}
      </select>
    </div>
  {:else if step.kind === 'scene'}
    <select bind:value={step.scene} aria-label={t('Szene')}>
      <option value="">{t('Szene wählen …')}</option>
      {#each scenes as sc (sc.id)}<option value={sc.id}>🎬 {sc.name}</option>{/each}
    </select>
    {#if !scenes.length}<span class="help">{t('Noch keine Szenen – lege zuerst eine Regel mit dem Auslöser „ich sie starte – eine Szene“ an.')}</span>{/if}
  {:else if step.kind === 'wait'}
    <label class="wf-inline"><input type="number" min="1" max="1440" bind:value={step.minutes} class="wf-num" /> {t('Minuten warten')}</label>
  {:else if step.kind === 'if'}
    <WfCond bind:cond={step.if} {tiles} />
    <span class="help">{t('Danach folgen die Schritte für „dann“ und für „sonst“.')}</span>
  {/if}
</div>
