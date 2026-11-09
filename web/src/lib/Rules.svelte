<script>
  // Workflows: "when one of these happens – only if these hold – do these
  // steps". Heimdeck checks them every half minute and runs them with the
  // rights of this account. The editor shows a workflow as a list of blocks or
  // as a flow from top to bottom; the AI can draft one from a sentence, which
  // is only saved after checking it.
  import { get, post, put, del } from './api.js';
  import { toast } from './toast.svelte.js';
  import { t, i18n } from './i18n.svelte.js';
  import { confirmDialog } from './dialog.svelte.js';
  import { blankWorkflow, blankTrigger, blankCond, editable, forSave, triggerText, triggerIcon, stepText, COOLDOWNS, cooldownName, DAYS, dayName } from './workflow.js';
  import WfTrigger from './WfTrigger.svelte';
  import WfCond from './WfCond.svelte';
  import WfSteps from './WfSteps.svelte';
  import WfFlow from './WfFlow.svelte';

  let { active = false, ai = null } = $props();

  let rules = $state([]);
  let log = $state([]);
  let values = $state({});
  let running = $state({});
  let telegram = $state(false);
  let tiles = $state(null);
  let editing = $state(null);
  let saving = $state(false);
  let starting = $state('');
  let windowOn = $state(false);
  let loaded = false;

  // List or flow, remembered in this browser.
  let view = $state(readView());
  function readView() {
    try { return localStorage.getItem('heimdeck-wf-view') === 'flow' ? 'flow' : 'list'; } catch { return 'list'; }
  }
  function setView(v) {
    view = v;
    try { localStorage.setItem('heimdeck-wf-view', v); } catch { /* private window */ }
  }

  async function load() {
    try {
      const r = await get('/rules');
      rules = r.rules;
      log = r.log;
      values = r.values;
      running = r.running || {};
      telegram = r.telegram;
    } catch (e) {
      toast(e.message, 'bad');
    }
  }
  $effect(() => {
    if (active && !loaded) {
      loaded = true;
      load();
    }
  });

  async function loadTiles() {
    if (tiles) return;
    try {
      tiles = await get('/rules/tiles');
    } catch (e) {
      toast(e.message, 'bad');
      tiles = [];
    }
  }

  const isManual = (r) => r.triggers?.some((x) => x.kind === 'manual');
  let scenes = $derived(rules.filter((r) => isManual(r) && r.id !== editing?.id));
  let autoTriggers = $derived(!!editing?.triggers.some((x) => x.kind !== 'manual'));
  let waitsForChange = $derived(!!editing?.triggers.some((x) => x.kind !== 'manual' && x.kind !== 'time'));
  // Whether one of the chosen buttons asks before it runs.
  let asks = $derived.by(() => {
    if (!editing) return false;
    let found = false;
    const walk = (list) => {
      for (const s of list || []) {
        if (s.kind === 'button' && s.confirm) found = true;
        walk(s.then);
        walk(s.else);
      }
    };
    walk(editing.steps);
    return found;
  });

  let aiText = $state('');
  let aiBusy = $state(false);
  let aiProblem = $state('');
  let dry = $state(null);
  let dryBusy = $state(false);

  function edit(r) {
    editing = editable(r || blankWorkflow());
    windowOn = !!(editing.only.from || editing.only.days?.length);
    aiText = '';
    aiProblem = '';
    dry = null;
    loadTiles();
  }

  function body() {
    const b = forSave($state.snapshot(editing));
    if (!windowOn || !autoTriggers) b.only = { from: '', to: '', days: [] };
    if (!b.name.trim()) b.name = b.triggers.map((x) => triggerText(x, tiles)).join(' / ').slice(0, 80);
    return b;
  }

  async function save() {
    saving = true;
    try {
      if (editing.id) await put(`/rules/${editing.id}`, body());
      else await post('/rules', body());
      editing = null;
      await load();
    } catch (e) {
      toast(e.message, 'bad');
    }
    saving = false;
  }

  async function askAI() {
    if (!aiText.trim()) return;
    aiBusy = true;
    aiProblem = '';
    try {
      const res = await post('/rules/draft', { text: aiText, current: editing.id ? body() : null });
      const keep = { id: editing.id, on: editing.on };
      editing = editable({ ...res.rule, ...keep });
      windowOn = !!(editing.only.from || editing.only.days?.length);
      aiProblem = res.problem || '';
      dry = null;
      toast(t('Entwurf übernommen – prüf ihn und speichere ihn dann.'));
    } catch (e) {
      toast(e.message, 'bad');
    }
    aiBusy = false;
  }

  async function dryRun() {
    dryBusy = true;
    try {
      const res = await post('/rules/dryrun', body());
      dry = { lines: res.lines, conds: res.conds };
    } catch (e) {
      toast(e.message, 'bad');
      dry = null;
    }
    dryBusy = false;
  }

  async function toggle(r) {
    try {
      await put(`/rules/${r.id}`, { ...$state.snapshot(r), on: !r.on });
      await load();
    } catch (e) {
      toast(e.message, 'bad');
    }
  }

  async function remove(r) {
    if (!(await confirmDialog(t('Die Regel „{name}“ löschen?', { name: r.name }), { danger: true, confirmLabel: t('Löschen') }))) return;
    try {
      await del(`/rules/${r.id}`);
      await load();
    } catch (e) {
      toast(e.message, 'bad');
    }
  }

  async function start(r) {
    starting = r.id;
    try {
      const e = await post(`/rules/${r.id}/run`);
      toast(e.result || t('Ausgeführt'), e.ok ? 'ok' : 'bad');
      await load();
    } catch (e) {
      toast(e.message, 'bad');
    }
    starting = '';
  }

  function toggleDay(d) {
    const i = editing.only.days.indexOf(d);
    if (i >= 0) editing.only.days.splice(i, 1);
    else editing.only.days.push(d);
  }
  const lastRun = (r) => log.find((e) => e.rule === r.id);
  const when = (unix) => new Date(unix * 1000).toLocaleString(i18n.lang, { dateStyle: 'short', timeStyle: 'short' });
</script>

<div class="rules">
  <p class="help">{t('Workflows der Form „Wenn …, nur wenn …, dann …“ – Heimdeck prüft sie jede halbe Minute und führt sie mit den Rechten deines Kontos aus. Die KI kann einen entwerfen; gespeichert und ausgeführt wird nur, was du selbst speicherst.')}</p>

  {#if !editing}
    {#each rules as r (r.id)}
      {@const last = lastRun(r)}
      <div class="rule" class:off={!r.on}>
        <label class="switch" title={r.on ? t('Ausschalten') : t('Einschalten')}>
          <input type="checkbox" checked={r.on} onchange={() => toggle(r)} aria-label={t('Regel an')} />
        </label>
        <div class="body">
          <strong>{r.name}{#if running[r.id]} <span class="badge">⏳ {t('läuft')}</span>{/if}</strong>
          <span class="muted small">{r.triggers.map((x) => triggerIcon(x.kind) + ' ' + triggerText(x)).join(' · ' + t('oder') + ' ')}{values[r.id] ? ` · ${t('jetzt')}: ${values[r.id]}` : ''}</span>
          {#if r.conds.length}<span class="muted small">✔ {r.conds.length === 1 ? t('1 Bedingung') : t('{n} Bedingungen', { n: r.conds.length })}</span>{/if}
          <span class="small">{r.steps.map((s) => stepText(s)).join(' → ')}</span>
          {#if last}<span class="muted small" class:bad={!last.ok}>{when(last.at)} – {last.result}</span>{/if}
        </div>
        <div class="row">
          <button class="small" onclick={() => start(r)} disabled={starting === r.id || !r.on && isManual(r)} title={t('Die Schritte jetzt ausführen – zählt als Bestätigung')}>{isManual(r) ? '🎬 ' + t('Starten') : '▶ ' + t('Jetzt')}</button>
          <button class="small" onclick={() => edit(r)}>{t('Bearbeiten')}</button>
          <button class="small ghost" onclick={() => remove(r)} aria-label={t('Löschen')} title={t('Löschen')}>✕</button>
        </div>
      </div>
    {:else}
      <p class="muted">{t('Noch keine Workflows. Zum Beispiel: Wenn der NAS-Speicher über 90 % ist und niemand streamt, Downloads pausieren und mir schreiben – oder eine Szene „Filmabend“ zum Starten von Hand.')}</p>
    {/each}
    <button class="primary small" onclick={() => edit(null)} disabled={rules.length >= 50}>＋ {t('Neuer Workflow')}</button>

    {#if log.length}
      <h4>{t('Protokoll')}</h4>
      <ul class="log">
        {#each log.slice(0, 20) as e (e.at + e.rule + e.result)}
          <li class:bad={!e.ok}><span class="muted">{when(e.at)}</span> <strong>{e.name}</strong> – {e.why}{e.why ? ': ' : ''}{e.result}</li>
        {/each}
      </ul>
    {/if}
  {:else}
    <div class="editor">
      <label class="field">
        <span>{t('Name')}</span>
        <input bind:value={editing.name} maxlength="80" placeholder={t('z. B. NAS fast voll')} />
      </label>

      {#if ai}
        <div class="ai">
          <span>✨ {editing.id ? t('Mit KI ändern') : t('Mit KI beschreiben')}</span>
          <textarea bind:value={aiText} rows="2" maxlength="2000" placeholder={editing.id ? t('z. B. Schick mir zusätzlich das Kamerabild und warte vorher 5 Minuten') : t('z. B. Wenn der NAS über 90 % voll ist und niemand streamt, pausiere SABnzbd und schick mir eine Nachricht')}></textarea>
          <div class="row wrap">
            <button class="small" onclick={askAI} disabled={aiBusy || !aiText.trim()}>{aiBusy ? t('Die KI denkt …') : editing.id ? t('Ändern lassen') : t('Entwurf erstellen')}</button>
            <span class="help">{t('Der Entwurf ersetzt, was hier steht – gespeichert wird erst, wenn du speicherst.')}</span>
          </div>
          {#if aiProblem}<span class="help bad">⚠ {aiProblem} {t('– bitte im Editor korrigieren.')}</span>{/if}
        </div>
      {/if}

      <div class="viewswitch" role="radiogroup" aria-label={t('Ansicht')}>
        <button class:on={view === 'list'} role="radio" aria-checked={view === 'list'} onclick={() => setView('list')}>☰ {t('Liste')}</button>
        <button class:on={view === 'flow'} role="radio" aria-checked={view === 'flow'} onclick={() => setView('flow')}>⑂ {t('Fluss')}</button>
      </div>
      {#if tiles === null}<p class="muted small">{t('Lade die Kacheln …')}</p>{/if}

      {#if view === 'flow'}
        <WfFlow bind:wf={editing} tiles={tiles || []} {scenes} {ai} />
      {:else}
        <h4>{t('Wenn …')} <span class="sub">{t('einer davon reicht')}</span></h4>
        {#each editing.triggers as tr, i (i)}
          <div class="block">
            {#if editing.triggers.length > 1}<button class="small ghost x" onclick={() => editing.triggers.splice(i, 1)} aria-label={t('Entfernen')} title={t('Entfernen')}>✕</button>{/if}
            <WfTrigger bind:tr={editing.triggers[i]} tiles={tiles || []} />
          </div>
        {/each}
        {#if editing.triggers.length < 5}<button class="small" onclick={() => editing.triggers.push(blankTrigger())}>＋ {t('Auslöser')}</button>{/if}

        <h4>{t('Nur wenn …')} <span class="sub">{t('alle müssen gelten – optional')}</span></h4>
        {#each editing.conds as c, i (i)}
          <div class="block">
            <button class="small ghost x" onclick={() => editing.conds.splice(i, 1)} aria-label={t('Entfernen')} title={t('Entfernen')}>✕</button>
            <WfCond bind:cond={editing.conds[i]} tiles={tiles || []} />
          </div>
        {/each}
        {#if editing.conds.length < 5}<button class="small" onclick={() => editing.conds.push(blankCond())}>＋ {t('Bedingung')}</button>{/if}

        <h4>{t('… dann')} <span class="sub">{t('der Reihe nach')}</span></h4>
        <WfSteps bind:steps={editing.steps} tiles={tiles || []} {scenes} {ai} />
      {/if}

      <h4>{t('Einstellungen')}</h4>
      {#if autoTriggers}
        <label class="check"><input type="checkbox" bind:checked={windowOn} /> {t('Nur zu bestimmten Zeiten')}</label>
        {#if windowOn}
          <div class="row wrap">
            <label class="inline">{t('Zeitfenster von')} <input type="time" bind:value={editing.only.from} /></label>
            <label class="inline">{t('bis')} <input type="time" bind:value={editing.only.to} /></label>
            <div class="wf-days" role="group" aria-label={t('Wochentage')}>
              {#each DAYS as d (d)}<button type="button" class="small" class:on={editing.only.days.includes(d)} onclick={() => toggleDay(d)}>{dayName(d)}</button>{/each}
            </div>
          </div>
        {/if}
      {/if}
      {#if waitsForChange}
        <label class="field">
          <span>{t('Frühestens wieder nach')}</span>
          <select bind:value={editing.cooldown}>
            {#each COOLDOWNS as m (m)}<option value={m}>{cooldownName(m)}</option>{/each}
          </select>
          <span class="help">{t('Der Workflow startet erst wieder, wenn sein Auslöser zwischendurch nicht mehr galt und diese Zeit vergangen ist.')}</span>
        </label>
      {/if}
      {#if asks}
        <label class="check"><input type="checkbox" bind:checked={editing.noAsk} /> {t('Ohne Nachfrage ausführen')}</label>
        <span class="help" class:bad={!editing.noAsk && !telegram}>
          {editing.noAsk
            ? t('Der Knopf läuft sofort, ohne dass jemand bestätigt.')
            : telegram
              ? t('Ein Knopf fragt auf dem Dashboard nach – der Workflow fragt dich deshalb in Telegram (✅ / Abbrechen) und wartet auf deine Antwort, bevor er ihn drückt.')
              : t('Dieser Knopf fragt auf dem Dashboard nach. Die Regel fragt dafür in Telegram – richte unter Benachrichtigungen Telegram ein, sonst läuft der Knopf nicht.')}
        </span>
      {/if}

      {#if dry}
        <div class="dry">
          <strong>{t('Probelauf – so würde es jetzt laufen')}</strong>
          {#if !dry.conds}<span class="bad small">{t('Die Bedingungen gelten gerade nicht – ein Auslöser würde jetzt nichts starten.')}</span>{/if}
          <ul>{#each dry.lines as l, i (i)}<li>{l}</li>{/each}</ul>
        </div>
      {/if}

      <div class="row end wrap">
        <button class="small" onclick={dryRun} disabled={dryBusy}>🧪 {t('Probelauf')}</button>
        <span class="grow"></span>
        <button class="small" onclick={() => (editing = null)}>{t('Abbrechen')}</button>
        <button class="small primary" onclick={save} disabled={saving}>{t('Speichern')}</button>
      </div>
    </div>
  {/if}
</div>

<style>
  .rules { display: grid; gap: 10px; }
  .rule { display: flex; gap: 10px; align-items: flex-start; padding: 10px 12px; border: 1px solid var(--border); border-radius: var(--r-sm); background: var(--surface-2); }
  .rule.off .body { opacity: .55; }
  .rule .body { flex: 1; min-width: 0; display: grid; gap: 2px; overflow-wrap: anywhere; }
  .switch { padding-top: 2px; }
  .badge { font-size: 11px; font-weight: 400; padding: 1px 6px; border-radius: 8px; background: var(--surface); border: 1px solid var(--border); }
  .row { display: flex; gap: 6px; align-items: center; }
  .wrap { flex-wrap: wrap; }
  .end { justify-content: flex-end; }
  .grow { flex: 1; }
  .bad { color: var(--bad-text, var(--bad)); }
  h4 { margin: 10px 0 0; font-size: 13px; text-transform: uppercase; letter-spacing: .04em; color: var(--muted); }
  h4 .sub { text-transform: none; letter-spacing: 0; font-weight: 400; }
  .editor { display: grid; gap: 8px; padding: 12px; border: 1px solid var(--border); border-radius: var(--r-sm); }
  .ai { display: grid; gap: 6px; padding: 10px; border-radius: var(--r-sm); background: color-mix(in srgb, var(--accent) 8%, var(--surface-2)); }
  .viewswitch { display: inline-flex; justify-self: start; border: 1px solid var(--border); border-radius: var(--r-sm); overflow: hidden; }
  .viewswitch button { border: 0; border-radius: 0; padding: 6px 14px; background: none; }
  .viewswitch button.on { background: var(--accent); color: var(--on-accent, #fff); }
  .block { position: relative; padding: 8px 36px 8px 8px; border-radius: var(--r-sm); background: var(--surface-2); }
  .block .x { position: absolute; top: 6px; right: 6px; }
  .inline { display: inline-flex; gap: 6px; align-items: center; }
  .dry { display: grid; gap: 4px; padding: 10px; border: 1px dashed var(--border); border-radius: var(--r-sm); font-size: 13px; }
  .dry ul { margin: 0; padding-left: 18px; }
  .log { list-style: none; margin: 0; padding: 0; display: grid; gap: 4px; font-size: 13px; }
  .log li { overflow-wrap: anywhere; }
  /* Shared by the blocks of both views. */
  :global(.wf-fields) { display: grid; gap: 6px; }
  :global(.wf-row) { display: flex; flex-wrap: wrap; gap: 6px; align-items: center; }
  :global(.wf-row > select) { flex: 1 1 180px; width: auto; min-width: 0; }
  :global(.wf-days) { display: flex; gap: 4px; flex-wrap: wrap; }
  :global(.wf-days button) { min-width: 38px; justify-content: center; }
  :global(.wf-days .on) { border-color: var(--accent); color: var(--accent-text); }
  :global(.wf-num) { width: 100px; }
  :global(.wf-op) { flex: 0 0 auto !important; width: auto; }
  :global(.wf-inline) { display: inline-flex; gap: 6px; align-items: center; }
  :global(.wf-steps) { display: grid; gap: 6px; }
  :global(.wf-block) { display: grid; gap: 6px; padding: 8px; border-radius: var(--r-sm); background: var(--surface-2); border: 1px solid var(--border); }
  :global(.wf-block.if) { border-color: var(--warn, #d9a400); }
  :global(.wf-block-head) { display: flex; gap: 4px; align-items: center; }
  :global(.wf-block-head .grow) { flex: 1; }
  :global(.wf-n) { font-size: 12px; color: var(--muted); }
  :global(.wf-branches) { display: grid; grid-template-columns: repeat(auto-fit, minmax(220px, 1fr)); gap: 8px; }
  :global(.wf-branch) { display: grid; gap: 6px; padding: 8px; border-radius: var(--r-sm); border: 1px dashed var(--border); }
  :global(.wf-blabel) { font-size: 11px; text-transform: uppercase; letter-spacing: .06em; color: var(--muted); }
  @media (max-width: 640px) {
    .rule { flex-wrap: wrap; }
    .rule .row { width: 100%; justify-content: flex-end; }
  }
</style>
