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
  import WfSentence from './WfSentence.svelte';
  import WfCanvas from './WfCanvas.svelte';
  import { portal } from './portal.js';

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

  // Simple (a sentence), or advanced as a flow or a list – and whether the
  // editor fills the page; both remembered in this browser.
  let view = $state(readPref('heimdeck-wf-view', 'simple', ['simple', 'flow', 'list']));
  let big = $state(readPref('heimdeck-wf-big', '', ['1']) === '1');
  let advanced = $state(readPref('heimdeck-wf-adv', 'flow', ['flow', 'list']));
  function readPref(key, fallback, allowed) {
    try {
      const v = localStorage.getItem(key);
      return allowed.includes(v) ? v : fallback;
    } catch {
      return fallback;
    }
  }
  function writePref(key, v) {
    try { localStorage.setItem(key, v); } catch { /* private window */ }
  }
  function setView(v) {
    view = v;
    if (v !== 'simple') advanced = v;
    writePref('heimdeck-wf-view', v);
    if (v !== 'simple') writePref('heimdeck-wf-adv', v);
  }
  function setBig(v) {
    big = v;
    writePref('heimdeck-wf-big', v ? '1' : '');
  }
  // Esc makes the large editor small again (and does not close the settings).
  $effect(() => {
    if (!editing || !big) return;
    const onKey = (e) => {
      if (e.key !== 'Escape' || document.querySelector('.backdrop.top')) return;
      const el = document.activeElement;
      if (el?.matches?.('input, textarea, select')) return;
      e.preventDefault();
      e.stopPropagation();
      setBig(false);
    };
    window.addEventListener('keydown', onKey, true);
    return () => window.removeEventListener('keydown', onKey, true);
  });

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

  // The large advanced view is the canvas: it gets the whole height, the AI box opens on request.
  let canvas = $derived(big && view === 'flow');
  let aiOpen = $state(false);
  let aiText = $state('');
  let aiBusy = $state(false);
  let aiProblem = $state('');
  let dry = $state(null);
  let dryBusy = $state(false);

  // What would happen now, kept up to date while editing (without asking the
  // AI watcher, which may cost money – the 🧪 button does that).
  let live = $state(null);
  let liveSeq = 0;
  $effect(() => {
    if (!editing || tiles === null) return;
    const b = body();
    const seq = ++liveSeq;
    const timer = setTimeout(async () => {
      try {
        const res = await post('/rules/dryrun?quiet=1', b);
        if (seq === liveSeq) live = { lines: res.lines, conds: res.conds, condList: res.condList, triggers: res.triggers, branches: res.branches };
      } catch (e) {
        if (seq === liveSeq) live = { problem: e.message };
      }
    }, 900);
    return () => clearTimeout(timer);
  });

  function edit(r) {
    editing = editable(r || blankWorkflow());
    windowOn = !!(editing.only.from || editing.only.days?.length);
    aiText = '';
    aiProblem = '';
    dry = null;
    live = null;
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
  const when = (unix) => new Date(unix * 1000).toLocaleString(i18n.lang, { dateStyle: 'short', timeStyle: 'short' });
</script>

{#snippet details()}
      {#if live}
        <div class="live" class:wait={live.problem}>
          {#if live.problem}
            <span>✎ {t('Noch nicht fertig:')} {live.problem}</span>
          {:else}
            <span class="muted">{t('Jetzt würde passieren:')}</span>
            {#if !live.conds}<span class="bad">{t('nichts – die Bedingungen gelten gerade nicht')}</span>{/if}
            <span class="flowline">{live.lines.join('\n')}</span>
          {/if}
        </div>
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
{/snippet}

<div class="rules">
  <p class="help">{t('Workflows der Form „Wenn …, nur wenn …, dann …“ – Heimdeck prüft sie jede halbe Minute und führt sie mit den Rechten deines Kontos aus. Die KI kann einen entwerfen; gespeichert und ausgeführt wird nur, was du selbst speicherst.')}</p>

  {#if !editing}
    {#each rules as r (r.id)}
      <div class="rule" class:off={!r.on}>
        <label class="switch" title={r.on ? t('Ausschalten') : t('Einschalten')}>
          <input type="checkbox" checked={r.on} onchange={() => toggle(r)} aria-label={t('Regel an')} />
        </label>
        <div class="body">
          <strong>{r.name}{#if running[r.id]} <span class="badge">⏳ {t('läuft')}</span>{/if}</strong>
          <span class="muted small">{r.triggers.map((x) => triggerIcon(x.kind) + ' ' + triggerText(x)).join(' · ' + (r.all ? t('und') : t('oder')) + ' ')}{values[r.id] ? ` · ${t('jetzt')}: ${values[r.id]}` : ''}</span>
          {#if r.conds.length}<span class="muted small">✔ {r.conds.length === 1 ? t('1 Bedingung') : t('{n} Bedingungen', { n: r.conds.length })}</span>{/if}
          <span class="small">{r.steps.map((s) => stepText(s)).join(' → ')}</span>
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
          <li class:bad={!e.ok}>
            <span class="muted">{when(e.at)}</span> <strong>{e.name}</strong>
            {#if !e.ok}<span class="errbadge" title={t('Mindestens ein Schritt hat nicht geklappt – die Zeilen mit ⚠ sagen, welcher und warum.')}>⚠ {t('Fehler')}</span>{/if}
            – {e.why}{e.why ? ': ' : ''}{e.result}
          </li>
        {/each}
      </ul>
    {/if}
  {:else}
    <div class="editor" class:big class:cv={canvas} use:portal={big}>
      <div class="ed-bar">
        <input class="ed-name" bind:value={editing.name} maxlength="80" placeholder={t('Name, z. B. NAS fast voll')} aria-label={t('Name')} />
        <div class="viewswitch" role="radiogroup" aria-label={t('Ansicht')}>
          <button class:on={view === 'simple'} role="radio" aria-checked={view === 'simple'} onclick={() => setView('simple')}>{t('Einfach')}</button>
          <button class:on={view !== 'simple'} role="radio" aria-checked={view !== 'simple'} onclick={() => setView(advanced)}>{t('Erweitert')}</button>
        </div>
        {#if view !== 'simple'}
          <button class="small ghost" onclick={() => setView(view === 'flow' ? 'list' : 'flow')} title={t('Darstellung wechseln')}>{view === 'flow' ? '☰ ' + t('als Liste') : '⑂ ' + t('als Fluss')}</button>
        {/if}
        <span class="grow"></span>
        {#if big}
          <!-- Large: only back to the small view – closing (and losing the changes) goes from there. -->
          {#if ai && canvas}<button class="small ghost" class:on={aiOpen} onclick={() => (aiOpen = !aiOpen)}>✨ {t('Mit KI')}</button>{/if}
          <button class="small" onclick={dryRun} disabled={dryBusy}>🧪 {t('Probelauf')}</button>
          <button class="small primary" onclick={save} disabled={saving}>{t('Speichern')}</button>
          <button class="small" onclick={() => setBig(false)} title={t('Verkleinern (Esc)')}>⤡ {t('Verkleinern')}</button>
        {:else}
          <button class="small ghost" onclick={() => setBig(true)} title={t('Auf die ganze Seite vergrößern')}>⤢ {t('Vollbild')}</button>
          <button class="small ghost icon" onclick={() => (editing = null)} aria-label={t('Schließen ohne Speichern')} title={t('Schließen ohne Speichern')}>✕</button>
        {/if}
      </div>

      {#if ai && (!canvas || aiOpen)}
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

      {#if tiles === null}<p class="muted small">{t('Lade die Kacheln …')}</p>{/if}

      {#if view === 'simple'}
        <WfSentence bind:wf={editing} tiles={tiles || []} {scenes} {ai} />
      {:else if view === 'flow'}
        <!-- The same canvas in the window and on the whole page; there it also holds the settings. -->
        <WfCanvas bind:wf={editing} tiles={tiles || []} {scenes} {ai} {live} fill={canvas} panel={canvas ? details : null} />
      {:else}
        <h4>{t('Wenn …')}
          {#if editing.triggers.length > 1}
            <select class="joinsel" bind:value={editing.all} aria-label={t('Verknüpfung der Auslöser')}>
              <option value={false}>{t('oder – einer davon reicht')}</option>
              <option value={true}>{t('und – alle zugleich')}</option>
            </select>
          {/if}
        </h4>
        {#each editing.triggers as tr, i (i)}
          <div class="block">
            {#if editing.triggers.length > 1}<button class="small ghost x" onclick={() => editing.triggers.splice(i, 1)} aria-label={t('Entfernen')} title={t('Entfernen')}>✕</button>{/if}
            <WfTrigger bind:tr={editing.triggers[i]} tiles={tiles || []} {ai} />
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

      {#if !canvas}{@render details()}{/if}

      <div class="row end wrap" class:hide={big}>
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
  .joinsel { width: auto; margin-left: 8px; text-transform: none; font-size: 12px; padding: 2px 6px; }
  .editor { display: grid; gap: 8px; padding: 12px; border: 1px solid var(--border); border-radius: var(--r-sm); align-content: start; }
  .editor.big {
    position: fixed; inset: 0; z-index: 60; overflow-y: auto; border: 0; border-radius: 0; gap: 10px;
    background: var(--surface-solid, var(--bg)); color: var(--text); font-family: var(--font);
    padding: 16px 20px 32px;
  }
  .editor.big.cv { height: 100vh; height: 100dvh; overflow: hidden; display: flex; flex-direction: column; padding-bottom: 12px; }
  .editor.big.cv .ed-bar { margin: -16px 0 0; flex: none; }
  .editor.big.cv > :global(*) { flex: none; }
  .editor.big.cv > :global(.canvas) { flex: 1 1 auto; min-height: 0; }
  .hide { display: none; }
  .ed-bar { display: flex; flex-wrap: wrap; gap: 8px; align-items: center; }
  .editor.big .ed-bar { position: sticky; top: -16px; z-index: 2; margin: -16px 0 0; padding: 12px 0; background: var(--surface-solid, var(--bg)); border-bottom: 1px solid var(--border); }
  .ed-name { flex: 1 1 220px; min-width: 0; font-size: 15px; font-weight: 600; }
  .icon { font-size: 16px; width: 34px; justify-content: center; }
  .live { display: grid; gap: 4px; padding: 8px 10px; border-radius: var(--r-sm); background: color-mix(in srgb, #1d9e75 10%, var(--surface-2)); font-size: 13px; }
  .live.wait { background: var(--surface-2); color: var(--muted); }
  .flowline { white-space: pre-wrap; overflow-wrap: anywhere; }
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
  .errbadge { display: inline-block; font-size: 11px; font-weight: 600; padding: 0 6px; border-radius: 8px; color: var(--bad-text, var(--bad)); border: 1px solid currentColor; vertical-align: 1px; }
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
