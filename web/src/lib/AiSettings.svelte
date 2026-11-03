<script>
  // The AI that answers in the search window (admin only): any
  // OpenAI-compatible server – Ollama, LM Studio, LocalAI, OpenAI, OpenRouter …
  // The key is never sent back; an empty field keeps the stored one.
  import { get, put, post } from './api.js';
  import { toast } from './toast.svelte.js';
  import { t } from './i18n.svelte.js';

  let cfg = $state(null);
  let key = $state('');
  let keySet = $state(false);
  let models = $state([]);
  let checking = $state(false);
  let saving = $state(false);
  let searxBusy = $state(false);
  let searxResult = $state(null);

  async function load() {
    try {
      const res = await get('/ai');
      cfg = { url: '', model: '', label: '', searxUrl: '', ...res.config };
      keySet = res.keySet;
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
      if (!cfg.model && models.length) cfg.model = models[0];
    } catch (e) {
      toast(e.message, 'bad');
    } finally {
      checking = false;
    }
  }

  // A real search through the SearXNG address, so the admin sees right away
  // whether it lets Heimdeck in (public instances usually do not).
  async function testSearx() {
    searxBusy = true;
    try {
      const res = await post('/ai/searx', $state.snapshot(cfg));
      searxResult = { ok: res.results > 0, text: res.results > 0 ? t('Funktioniert – {n} Ergebnisse gefunden.', { n: res.results }) : t('Erreichbar, aber keine Ergebnisse – sind in SearXNG Suchmaschinen eingeschaltet?') };
    } catch (e) {
      searxResult = { ok: false, text: e.message };
    } finally {
      searxBusy = false;
    }
  }

  async function save(e) {
    e.preventDefault();
    saving = true;
    try {
      await put('/ai', { ...$state.snapshot(cfg), apiKey: key });
      key = '';
      toast(t('Gespeichert'));
      load();
    } catch (err) {
      toast(err.message, 'bad');
    } finally {
      saving = false;
    }
  }
</script>

{#if cfg}
  <form class="ai" onsubmit={save}>
    <p class="help">{t('Mit einer KI lassen sich Fragen direkt im Suchfenster stellen – die Treffer aus deinen Diensten gehen als Kontext mit. Es geht jede Schnittstelle im OpenAI-Format: selbst gehostet (Ollama, LM Studio, LocalAI) oder per API (OpenAI, OpenRouter, Mistral …).')}</p>
    <label class="field">
      <span>{t('Adresse')}</span>
      <input bind:value={cfg.url} placeholder="http://ollama:11434/v1" spellcheck="false" />
      <span class="help">{t('Beispiele:')} {#each EXAMPLES as [name, url], i}{i ? ', ' : ''}<button type="button" class="link" onclick={() => (cfg.url = url)}>{name}</button>{/each}. {t('Bei Ollama mit /v1 am Ende.')}</span>
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
    <label class="field">
      <span>{t('Websuche über SearXNG (optional)')}</span>
      <div class="row">
        <input bind:value={cfg.searxUrl} placeholder="http://searxng:8080" spellcheck="false" oninput={() => (searxResult = null)} />
        <button type="button" class="small" disabled={searxBusy || !cfg.searxUrl} onclick={testSearx}>{searxBusy ? t('Prüfe …') : t('Testen')}</button>
      </div>
      {#if searxResult}<span class="help" class:ok={searxResult.ok} class:bad={!searxResult.ok}>{searxResult.text}</span>{/if}
      <span class="help">{t('Die KI selbst kann nicht ins Internet. Mit einer SearXNG-Adresse sucht Heimdeck auf Wunsch (🌐 im Chat) und gibt ihr die Ergebnisse mit. In SearXNG unter search → formats „json“ erlauben.')} {t('Öffentliche SearXNG-Instanzen lassen das meist nicht zu – am besten eine eigene betreiben.')}</span>
    </label>
    <label class="check"><input type="checkbox" bind:checked={cfg.adminsOnly} /> {t('Nur Admins dürfen fragen')}</label>
    <span class="help">{t('Sinnvoll bei Cloud-Anbietern, die pro Anfrage kosten.')}</span>
    <label class="check"><input type="checkbox" bind:checked={cfg.insecure} /> {t('Zertifikat nicht prüfen (selbstsigniert)')}</label>
    <p class="help">🔒 {t('Die KI sieht nur die Treffer, die die fragende Person ohnehin sieht. Knöpfe (Licht, Downloads …) führt sie nie aus. Bei Cloud-Anbietern verlassen die Treffer dein Zuhause – für maximale Privatsphäre ein lokales Modell nehmen.')}</p>
    <label class="check"><input type="checkbox" bind:checked={cfg.enabled} /> <b>{t('KI im Suchfenster einschalten')}</b></label>
    <div class="row"><span class="spacer"></span><button class="primary small" disabled={saving}>{t('Speichern')}</button></div>
  </form>
{/if}

<style>
  .ai { display: grid; gap: 12px; }
  .two { display: grid; grid-template-columns: 1fr 1fr; gap: 10px; }
  @media (max-width: 600px) { .two { grid-template-columns: 1fr; } }
  .row { display: flex; gap: 8px; align-items: center; }
  .row input { flex: 1; min-width: 0; }
  .spacer { flex: 1; }
  .help.ok { color: var(--ok); }
  .help.bad { color: var(--bad); }
  .link { border: 0; background: none; padding: 0; color: var(--accent); font-size: inherit; text-decoration: underline; cursor: pointer; }
</style>
