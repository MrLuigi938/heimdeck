<script>
  // The AI that answers in the search window (admin only): any
  // OpenAI-compatible server – Ollama, LM Studio, LocalAI, OpenAI, OpenRouter …
  // The key is never sent back; an empty field keeps the stored one.
  import { get, put, post } from './api.js';
  import { toast } from './toast.svelte.js';
  import { t } from './i18n.svelte.js';

  let { onSaved = () => {} } = $props();

  let cfg = $state(null);
  let key = $state('');
  let keySet = $state(false);
  let webKey = $state('');
  let webKeySet = $state(false);
  let webSaved = $state(''); // the provider the stored key belongs to
  let models = $state([]);
  let checking = $state(false);
  let saving = $state(false);
  let searxBusy = $state(false);
  let searxResult = $state(null);

  async function load() {
    try {
      const res = await get('/ai');
      cfg = { url: '', model: '', label: '', searxUrl: '', ...res.config };
      cfg.webSearch ||= 'none';
      keySet = res.keySet;
      webKeySet = res.webKeySet;
      webSaved = cfg.webSearch;
    } catch (e) {
      toast(e.message, 'bad');
    }
  }
  load();

  const EXAMPLES = [
    ['Ollama', 'http://ollama:11434/v1'],
    ['LM Studio', 'http://192.168.1.20:1234/v1'],
    ['OpenAI', 'https://api.openai.com/v1'],
    ['OpenRouter', 'https://openrouter.ai/api/v1'],
  ];

  async function check() {
    checking = true;
    try {
      models = (await post('/ai/models', { ...$state.snapshot(cfg), apiKey: key })).models;
      toast(models.length ? t('Verbunden – {n} Modelle gefunden.', { n: models.length }) : t('Verbunden, aber keine Modelle gefunden.'));
      if (!cfg.model && models.length) {
        cfg.model = models[0];
        scheduleSave();
      }
    } catch (e) {
      toast(e.message, 'bad');
    } finally {
      checking = false;
    }
  }

  // The web search services. Brave and Tavily need an API key; OpenRouter
  // searches by itself, so it only works with an OpenRouter address.
  const WEB = [
    ['none', 'Keine'],
    ['searxng', 'SearXNG (selbst gehostet)'],
    ['brave', 'Brave Search API'],
    ['tavily', 'Tavily'],
    ['openrouter', 'OpenRouter (eingebaute Websuche)'],
  ];
  const WEB_HELP = {
    none: 'Die KI selbst kann nicht ins Internet. Mit einer Websuche sucht Heimdeck auf Wunsch (🌐 im Chat) und gibt ihr die Ergebnisse mit.',
    searxng: 'Eine eigene SearXNG, z. B. als Docker-Container. In ihrer settings.yml unter search → formats „json“ erlauben. Öffentliche SearXNG-Instanzen lassen das meist nicht zu.',
    brave: 'Eigener Suchindex von Brave. Den API-Schlüssel gibt es unter api-dashboard.search.brave.com – mit einem kleinen kostenlosen Kontingent.',
    tavily: 'Eine Such-API für KI-Assistenten. Den API-Schlüssel (tvly-…) gibt es unter app.tavily.com – mit einem kostenlosen Kontingent pro Monat.',
    openrouter: 'OpenRouter sucht selbst und gibt der KI die Ergebnisse mit – das kostet bei OpenRouter etwas pro Frage zusätzlich. Nur, wenn oben OpenRouter eingetragen ist.',
  };
  let needsKey = $derived(cfg?.webSearch === 'brave' || cfg?.webSearch === 'tavily');
  let keyStored = $derived(webKeySet && webSaved === cfg?.webSearch);
  // A web search that still lacks something waits with saving (instead of
  // an error the moment it is picked) and says what is missing.
  let webMissing = $derived(
    !cfg ? '' :
    cfg.webSearch === 'searxng' && !cfg.searxUrl.trim() ? t('Wird gespeichert, sobald die SearXNG-Adresse eingetragen ist.') :
    needsKey && !webKey && !keyStored ? t('Wird gespeichert, sobald der API-Schlüssel eingetragen ist.') :
    cfg.webSearch === 'openrouter' && !/openrouter\.ai/i.test(cfg.url) ? t('Geht nur, wenn oben OpenRouter als Adresse eingetragen ist.') : ''
  );

  // A real search with the chosen service, so the admin sees right away
  // whether it works (public SearXNG instances usually do not let Heimdeck in).
  async function testSearx() {
    searxBusy = true;
    try {
      const res = await post('/ai/websearch', { ...$state.snapshot(cfg), webKey });
      searxResult = res.provider === 'openrouter'
        ? { ok: true, text: t('Passt – OpenRouter sucht bei jeder Frage mit 🌐 selbst.') }
        : { ok: res.results > 0, text: res.results > 0 ? t('Funktioniert – {n} Ergebnisse gefunden.', { n: res.results }) : t('Erreichbar, aber keine Ergebnisse – sind in SearXNG Suchmaschinen eingeschaltet?') };
    } catch (e) {
      searxResult = { ok: false, text: e.message };
    } finally {
      searxBusy = false;
    }
  }

  // Saved right away like every other setting: checkboxes and selects when
  // they change, text fields when they are left (or Enter). Keys go along only
  // when something was typed into their field.
  let saveTimer;
  function scheduleSave() {
    clearTimeout(saveTimer);
    saveTimer = setTimeout(save, 300);
  }
  async function save(e) {
    e?.preventDefault?.();
    clearTimeout(saveTimer);
    if (webMissing) return;
    if (saving) return scheduleSave();
    saving = true;
    const sentKey = key, sentWebKey = webKey;
    try {
      await put('/ai', { ...$state.snapshot(cfg), apiKey: sentKey, webKey: sentWebKey });
      if (sentKey) {
        keySet = true;
        if (key === sentKey) key = '';
      }
      if (sentWebKey) {
        webKeySet = true;
        webSaved = cfg.webSearch;
        if (webKey === sentWebKey) webKey = '';
      }
      onSaved();
    } catch (err) {
      toast(err.message, 'bad');
    } finally {
      saving = false;
    }
  }
</script>

{#if cfg}
  <form class="ai" onsubmit={save} onchange={scheduleSave}>
    <p class="help">{t('Mit einer KI lassen sich Fragen direkt im Suchfenster stellen – die Treffer aus deinen Diensten gehen als Kontext mit. Es geht jede Schnittstelle im OpenAI-Format: selbst gehostet (Ollama, LM Studio, LocalAI) oder per API (OpenAI, OpenRouter, Mistral …).')}</p>
    <label class="field">
      <span>{t('Adresse')}</span>
      <input bind:value={cfg.url} placeholder="http://ollama:11434/v1" spellcheck="false" />
      <span class="help">{t('Beispiele:')} {#each EXAMPLES as [name, url], i}{i ? ', ' : ''}<button type="button" class="link" onclick={() => { cfg.url = url; scheduleSave(); }}>{name}</button>{/each}. {t('Bei Ollama mit /v1 am Ende.')}</span>
    </label>
    <div class="two">
      <label class="field">
        <span>{t('API-Schlüssel (optional)')}</span>
        <input type="password" bind:value={key} placeholder={keySet ? t('gespeichert – leer lassen zum Behalten') : t('bei Ollama nicht nötig')} autocomplete="new-password" />
      </label>
      <label class="field">
        <span>{t('Modell')}</span>
        <div class="row">
          <input bind:value={cfg.model} list="ai-models" placeholder="llama3.1" spellcheck="false" />
          <button type="button" class="small" disabled={checking || !cfg.url} onclick={check}>{checking ? t('Prüfe …') : t('Modelle laden')}</button>
        </div>
        <datalist id="ai-models">{#each models as m}<option value={m}></option>{/each}</datalist>
      </label>
    </div>
    <label class="field">
      <span>{t('Name im Suchfenster')}</span>
      <input bind:value={cfg.label} placeholder={cfg.model || 'Ollama'} />
    </label>
    <div class="field web">
      <span>{t('Websuche über')}</span>
      <!-- The Test button sits next to the choice for every service; address or key below. -->
      <div class="row">
        <select bind:value={cfg.webSearch} onchange={() => (searxResult = null)} aria-label={t('Websuche über')}>
          {#each WEB as [v, label]}<option value={v}>{t(label)}</option>{/each}
        </select>
        {#if cfg.webSearch !== 'none'}
          <button type="button" class="small" disabled={searxBusy || (cfg.webSearch === 'searxng' && !cfg.searxUrl) || (needsKey && !webKey && !keyStored)} onclick={testSearx}>{searxBusy ? t('Prüfe …') : t('Testen')}</button>
        {/if}
      </div>
      {#if cfg.webSearch === 'searxng'}
        <input bind:value={cfg.searxUrl} placeholder="http://searxng:8080" spellcheck="false" aria-label={t('SearXNG-Adresse')} oninput={() => (searxResult = null)} />
      {:else if needsKey}
        <input type="password" bind:value={webKey} placeholder={keyStored ? t('gespeichert – leer lassen zum Behalten') : t('API-Schlüssel')} aria-label={t('API-Schlüssel')} autocomplete="new-password" oninput={() => (searxResult = null)} />
      {/if}
      {#if webMissing}<span class="help warn">{webMissing}</span>{/if}
      {#if searxResult}<span class="help" class:ok={searxResult.ok} class:bad={!searxResult.ok}>{searxResult.text}</span>{/if}
      <span class="help">{t(WEB_HELP[cfg.webSearch] || WEB_HELP.none)}</span>
    </div>
    <label class="check"><input type="checkbox" bind:checked={cfg.dashboard} /> {t('Dashboard-Daten an die KI geben (📋 im Chat)')}</label>
    {#if cfg.dashboard}
      <label class="check sub"><input type="checkbox" bind:checked={cfg.tools} /> {t('Die KI fragt selbst nach (Werkzeuge)')}</label>
      <span class="help sub">{t('Statt der ganzen Übersicht bekommt die KI Werkzeuge: Kacheln auflisten, eine Kachel lesen, in deinen Diensten suchen und Aktionen als Knopf vorschlagen – ausführen tut sie nie etwas, das macht nur ein Klick. Braucht ein Modell, das Werkzeuge (Function Calling) kann, z. B. die meisten bei OpenRouter oder Llama 3.1 / Qwen bei Ollama; sonst bekommt es automatisch die Übersicht.')}</span>
    {/if}
    <span class="help">{t('Wer 📋 im Chat einschaltet, gibt der KI mit, was die eigenen Kacheln gerade zeigen – Werte, Listen, Warnungen und welche Dienste nicht erreichbar sind. Nur Kacheln, die die Person selbst sieht; nie Einstellungen oder Schlüssel.')}</span>
    <label class="check"><input type="checkbox" bind:checked={cfg.adminsOnly} /> {t('Nur Admins dürfen fragen')}</label>
    <span class="help">{t('Sinnvoll bei Cloud-Anbietern, die pro Anfrage kosten.')}</span>
    <label class="check"><input type="checkbox" bind:checked={cfg.insecure} /> {t('Zertifikat nicht prüfen (selbstsigniert)')}</label>
    <p class="help">🔒 {t('Die KI sieht nur die Treffer, die die fragende Person ohnehin sieht. Knöpfe (Licht, Downloads …) führt sie nie aus. Bei Cloud-Anbietern verlassen die Treffer dein Zuhause – für maximale Privatsphäre ein lokales Modell nehmen.')}</p>
    <label class="check"><input type="checkbox" bind:checked={cfg.enabled} /> <b>{t('KI im Suchfenster einschalten')}</b></label>
  </form>
{/if}

<style>
  .ai { display: grid; gap: 12px; }
  .two { display: grid; grid-template-columns: 1fr 1fr; gap: 10px; }
  @media (max-width: 600px) { .two { grid-template-columns: 1fr; } }
  .row { display: flex; gap: 8px; align-items: center; }
  .row input, .row select { flex: 1; min-width: 0; }
  .web { display: grid; gap: 6px; font-size: 13px; color: var(--muted); }
  .spacer { flex: 1; }
  .help.ok { color: var(--ok); }
  .help.warn { color: var(--warn); }
  .sub { margin-left: 26px; }
  .help.bad { color: var(--bad); }
  .link { border: 0; background: none; padding: 0; color: var(--accent); font-size: inherit; text-decoration: underline; cursor: pointer; }
</style>
