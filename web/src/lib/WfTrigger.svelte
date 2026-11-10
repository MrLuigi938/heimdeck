<script>
  // One trigger of a workflow.
  import { t } from './i18n.svelte.js';
  import { DAYS, dayName, EVENTS, WATCH_EVERY, cooldownName } from './workflow.js';

  let { tr = $bindable(), tiles = [], ai = null } = $props();
  const tileById = (id) => tiles.find((x) => x.id === Number(id));
  let monitored = $derived(tiles.filter((x) => x.monitored));
  let withStats = $derived(tiles.filter((x) => x.stats.length));
  let cameras = $derived(tiles.filter((x) => x.kind === 'camera'));
  let hooks = $derived(tiles.filter((x) => x.kind === 'webhook'));
  let lists = $derived(tiles.filter((x) => x.list));
  function toggleDay(d) {
    const i = tr.days.indexOf(d);
    if (i >= 0) tr.days.splice(i, 1);
    else tr.days.push(d);
  }
</script>

<div class="wf-fields">
  <select bind:value={tr.kind} aria-label={t('Auslöser')}>
    <option value="time">🕑 {t('es eine bestimmte Uhrzeit ist')}</option>
    <option value="down">🔴 {t('ein Dienst nicht mehr antwortet')}</option>
    <option value="up">🟢 {t('ein Dienst wieder erreichbar ist')}</option>
    <option value="value">📈 {t('ein Wert einer Kachel eine Grenze überschreitet')}</option>
    <option value="item">📋 {t('in der Liste einer Kachel etwas Neues steht – oder etwas weg ist')}</option>
    <option value="camera">📹 {t('eine Kamera ein Ereignis meldet')}</option>
    <option value="webhook">📨 {t('eine Meldung an eine Webhook-Kachel kommt')}</option>
    {#if ai?.dashboard || tr.kind === 'ai'}<option value="ai">🤖 {t('der KI-Wächter etwas bemerkt')}</option>{/if}
    <option value="manual">🎬 {t('ich sie starte – eine Szene')}</option>
  </select>
  {#if tr.kind === 'time'}
    <div class="wf-row">
      <input type="time" bind:value={tr.time} aria-label={t('Uhrzeit')} />
      <div class="wf-days" role="group" aria-label={t('Wochentage')}>
        {#each DAYS as d (d)}<button type="button" class="small" class:on={tr.days.includes(d)} onclick={() => toggleDay(d)}>{dayName(d)}</button>{/each}
      </div>
    </div>
    <span class="help">{t('Ohne gewählten Tag: jeden Tag. Es gilt die Uhrzeit des Servers.')}</span>
  {:else if tr.kind === 'manual'}
    <span class="help">{t('Eine Szene startest du hier mit 🎬 Starten, in Telegram mit /szene, mit einer Szenen-Kachel auf dem Dashboard oder aus einer anderen Regel. Dein Start zählt als Bestätigung ihrer Knöpfe.')}</span>
  {:else if tr.kind === 'down' || tr.kind === 'up'}
    <div class="wf-row">
      <select bind:value={tr.tile} aria-label={t('Kachel')}>
        <option value={0}>{t('Kachel wählen …')}</option>
        {#each monitored as x (x.id)}<option value={x.id}>{x.name} ({x.group})</option>{/each}
      </select>
      {#if tr.kind === 'down'}<label class="wf-inline">{t('seit')} <input type="number" min="1" max="1440" bind:value={tr.for} class="wf-num" /> {t('Min.')}</label>{/if}
    </div>
  {:else if tr.kind === 'value'}
    <div class="wf-row">
      <select bind:value={tr.tile} onchange={() => (tr.stat = '')} aria-label={t('Kachel')}>
        <option value={0}>{t('Kachel wählen …')}</option>
        {#each withStats as x (x.id)}<option value={x.id}>{x.name} ({x.group})</option>{/each}
      </select>
      <select bind:value={tr.stat} disabled={!tr.tile} aria-label={t('Wert')}>
        <option value="">{t('Wert wählen …')}</option>
        {#each tileById(tr.tile)?.stats || [] as st (st.label)}<option value={st.label}>{st.label} ({st.value})</option>{/each}
      </select>
      <select bind:value={tr.op} class="wf-op" aria-label={t('Vergleich')}>
        <option value=">">{t('über')}</option>
        <option value="<">{t('unter')}</option>
      </select>
      <input bind:value={tr.value} placeholder="90 %" class="wf-num" aria-label={t('Grenze')} />
      <label class="wf-inline">{t('seit')} <input type="number" min="0" max="1440" bind:value={tr.for} class="wf-num" aria-label={t('Minuten')} /> {t('Min.')}</label>
    </div>
    <span class="help">{t('Die Grenze als Zahl, gern mit Einheit: 90 %, 100 GB, 2000 W. Größen wie GB und TB werden umgerechnet.')}</span>
  {:else if tr.kind === 'camera'}
    <div class="wf-row">
      <select bind:value={tr.tile} aria-label={t('Kamera')}>
        <option value={0}>{t('Kamera wählen …')}</option>
        {#each cameras as x (x.id)}<option value={x.id}>{x.name} ({x.group})</option>{/each}
      </select>
      <select bind:value={tr.event} aria-label={t('Ereignis')}>
        <option value="">{t('jedes Ereignis')}</option>
        {#each EVENTS as ev (ev)}<option value={ev}>{t(ev)}</option>{/each}
      </select>
    </div>
  {:else if tr.kind === 'ai'}
    <textarea bind:value={tr.prompt} rows="2" maxlength="500" aria-label={t('Anweisung an den KI-Wächter')} placeholder={t('z. B. Melde dich, wenn etwas ungewöhnlich ist – ein Dienst hängt, eine Platte fast voll ist oder Downloads seit Stunden stehen')}></textarea>
    <label class="wf-inline">{t('Nachsehen alle')}
      <select bind:value={tr.every} aria-label={t('Wie oft')} class="wf-op">
        {#each WATCH_EVERY as m (m)}<option value={m}>{cooldownName(m)}</option>{/each}
      </select>
    </label>
    <span class="help">{t('Die KI sieht dein Dashboard wie im Chat und meldet sich nur, wenn deine Anweisung zutrifft. Ein Workflow mit KI-Wächter schickt nur Nachrichten – Knöpfe und Szenen gehen hier nicht. Jede Frage an ein Cloud-Modell kostet.')}</span>
  {:else if tr.kind === 'item'}
    <div class="wf-row">
      <select bind:value={tr.tile} aria-label={t('Kachel')}>
        <option value={0}>{t('Kachel wählen …')}</option>
        {#each lists as x (x.id)}<option value={x.id}>{x.name} – {x.list}</option>{/each}
      </select>
      <select bind:value={tr.gone} class="wf-op" aria-label={t('Was')}>
        <option value={false}>{t('neuer Eintrag')}</option>
        <option value={true}>{t('Eintrag ist weg (z. B. Download fertig)')}</option>
      </select>
    </div>
    <input bind:value={tr.match} maxlength="100" placeholder={t('nur Einträge mit … (leer: alle)')} />
    <span class="help">{t('Der Eintrag steht in der Nachricht über den Baustein {Eintrag}.')}</span>
  {:else if tr.kind === 'webhook'}
    <div class="wf-row">
      <select bind:value={tr.tile} aria-label={t('Kachel')}>
        <option value={0}>{t('Kachel wählen …')}</option>
        {#each hooks as x (x.id)}<option value={x.id}>{x.name} ({x.group})</option>{/each}
      </select>
      <input bind:value={tr.match} maxlength="100" placeholder={t('nur wenn die Meldung enthält … (leer: jede)')} />
    </div>
  {/if}
</div>
