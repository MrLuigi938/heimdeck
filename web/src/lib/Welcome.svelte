<script>
  import { t } from './i18n.svelte.js';
  import { BUILTIN_BACKGROUNDS, builtinUrl, parseBackgrounds } from './backgrounds.js';

  // The welcome after the first sign-in: what Heimdeck is, a background to
  // start with, how services get onto the dashboard, and where the theme is
  // changed – then the short tour or straight in. Shown once (setting
  // onboarding=1, set by the server for new accounts); skipping ends it too.
  // background: the current setting; onBackground(value) saves a new one;
  // onDone(tour) closes it, with tour = true to start the tour.
  let { username = '', background = '', mode = 'dark', discover = '', onBackground, onDone } = $props();

  const STEPS = 4;
  let step = $state(0);
  // The built-in background chosen now ('' = none, null = an own picture that stays).
  let chosen = $derived.by(() => {
    const list = parseBackgrounds(background);
    if (!list.length) return '';
    return list.length === 1 && list[0].kind === 'builtin' ? list[0].id : null;
  });
  const pick = (id) => onBackground(id ? `builtin:${id}` : '');

  function key(e) {
    if (e.key === 'Escape') onDone(false);
    else if (e.key === 'ArrowRight' && step < STEPS - 1) step++;
    else if (e.key === 'ArrowLeft' && step > 0) step--;
  }
</script>

<svelte:window onkeydown={key} />

<div class="welcome" role="dialog" aria-modal="true" aria-labelledby="welcome-title">
  <div class="card box">
    <div class="dots" aria-label={t('Schritt {n} von {m}', { n: step + 1, m: STEPS })}>
      {#each { length: STEPS }, i (i)}<span class:on={i === step} class:done={i < step}></span>{/each}
      <button class="ghost skip" onclick={() => onDone(false)}>{t('Überspringen')}</button>
    </div>

    {#if step === 0}
      <div class="body">
        <img class="logo" src="/icon.svg" alt="" width="56" height="56" />
        <h2 id="welcome-title">{username ? t('Willkommen bei Heimdeck, {name}!', { name: username }) : t('Willkommen bei Heimdeck!')}</h2>
        <p>{t('Heimdeck ist deine Startseite fürs Heimnetz: alle Dienste an einem Ort – in drei kurzen Schritten ist alles eingerichtet.')}</p>
        <ul class="points">
          <li><span>🧩</span><div><b>{t('Dienste als Kacheln')}</b>{t('Ein Klick öffnet den Dienst; über 100 Dienste zeigen Live-Daten.')}</div></li>
          <li><span>🟢</span><div><b>{t('Immer im Blick')}</b>{t('Heimdeck prüft jede Minute, ob alles läuft, und meldet sich, wenn nicht.')}</div></li>
          <li><span>🔍</span><div><b>{t('Eine Suche für alles')}</b>{t('Dienste, Filme, Serien und Dokumente – mit / sofort zur Hand.')}</div></li>
        </ul>
      </div>
    {:else if step === 1}
      <div class="body">
        <h2 id="welcome-title">{t('Wähle deinen Hintergrund')}</h2>
        <p>{t('Er passt sich hellen und dunklen Themes an. Eigene Bilder oder Fotoalben gehen später unter Einstellungen → Aussehen.')}</p>
        <div class="bgs" role="radiogroup" aria-label={t('Hintergrund')}>
          {#each BUILTIN_BACKGROUNDS as b (b.id)}
            <button class="bg" class:on={chosen === b.id} role="radio" aria-checked={chosen === b.id} onclick={() => pick(b.id)}>
              <img src={builtinUrl(b.id, mode)} alt="" />
              <b>{b.name}</b><small>{t(b.text)}</small>
            </button>
          {/each}
          <button class="bg none" class:on={chosen === ''} role="radio" aria-checked={chosen === ''} onclick={() => pick('')}>
            <span class="plain"></span>
            <b>{t('Ohne')}</b><small>{t('Nur die Farbe des Themes')}</small>
          </button>
        </div>
        {#if chosen === null}<p class="help">{t('Du hast schon ein eigenes Hintergrundbild – es bleibt, solange du hier nichts anderes wählst.')}</p>{/if}
      </div>
    {:else if step === 2}
      <div class="body">
        <h2 id="welcome-title">{t('So kommen deine Dienste aufs Dashboard')}</h2>
        <ol class="ways">
          {#if discover}
            <li><b>{discover === 'docker' ? '🐳' : '🔍'} {t('Automatisch finden')}</b>{discover === 'docker' ? t('Heimdeck sieht deine Docker-Container und schlägt sie als Kacheln vor.') : t('Heimdeck schlägt deine Proxmox-VMs und -Container als Kacheln vor.')}</li>
          {/if}
          <li><b>✎ {t('Selbst hinzufügen')}</b>{t('Den Stift oben anklicken, dann „+ Kachel“ in einer Gruppe: Dienst aussuchen, Adresse eintragen – mit API-Key gibt es Live-Daten.')}</li>
          <li><b>⇪ {t('Übernehmen')}</b>{t('Kommst du von Heimdall, übernimmt Heimdeck dessen Export samt Tags als Gruppen.')}</li>
        </ol>
        <p class="help">{t('Gruppen ordnen deine Kacheln, z. B. Medien, Downloads, System – drei liegen schon bereit.')}</p>
      </div>
    {:else}
      <div class="body">
        <h2 id="welcome-title">{t('Fertig – fast!')}</h2>
        <p>{t('Theme, Akzentfarbe, Schrift und Abstände änderst du jederzeit unter ⚙ Einstellungen → Aussehen – es gibt 21 Themes, von schlicht bis verspielt.')}</p>
        <p>{t('Willst du eine kurze Tour? Sie zeigt in wenigen Schritten, wo Suche, Bearbeiten und Einstellungen sind.')}</p>
      </div>
    {/if}

    <div class="nav">
      {#if step > 0}<button onclick={() => step--}>← {t('Zurück')}</button>{/if}
      <span class="grow"></span>
      {#if step < STEPS - 1}
        <button class="primary" onclick={() => step++}>{step === 0 ? t("Los geht's") : t('Weiter')} →</button>
      {:else}
        <button onclick={() => onDone(false)}>{t('Direkt loslegen')}</button>
        <button class="primary" onclick={() => onDone(true)}>{t('Tour starten')}</button>
      {/if}
    </div>
  </div>
</div>

<style>
  .welcome { position: fixed; inset: 0; z-index: 60; display: grid; place-items: center; padding: 16px; background: color-mix(in srgb, var(--bg, #000) 35%, transparent); backdrop-filter: blur(3px); overflow-y: auto; }
  .box { width: min(640px, 100%); padding: 22px 26px 20px; display: grid; gap: 16px; background: var(--surface-solid); }
  .dots { display: flex; align-items: center; gap: 6px; }
  .dots span { width: 8px; height: 8px; border-radius: 50%; background: var(--border-strong); transition: width .2s; }
  .dots span.done { background: color-mix(in srgb, var(--accent) 55%, transparent); }
  .dots span.on { width: 22px; border-radius: 4px; background: var(--accent); }
  .skip { margin-left: auto; font-size: 13px; color: var(--muted); }
  .body { display: grid; gap: 12px; min-height: 290px; align-content: start; }
  .logo { border-radius: var(--r-md); }
  h2 { margin: 0; font-size: 22px; }
  p { margin: 0; line-height: 1.5; }
  .points, .ways { margin: 4px 0 0; padding: 0; list-style: none; display: grid; gap: 10px; }
  .points li { display: grid; grid-template-columns: 32px 1fr; gap: 10px; align-items: start; }
  .points li > span { font-size: 22px; line-height: 1.2; }
  .points b, .ways b { display: block; margin-bottom: 2px; }
  .points div, .ways li { color: var(--muted); font-size: 14px; line-height: 1.45; }
  .points b, .ways b { color: var(--text); }
  .ways li { background: var(--surface-2); border-radius: var(--r-md); padding: 10px 12px; }
  .bgs { display: grid; grid-template-columns: repeat(auto-fill, minmax(132px, 1fr)); gap: 10px; }
  .bg { display: grid; gap: 3px; padding: 6px 6px 8px; justify-items: start; text-align: left; border-radius: var(--r-md); background: var(--surface-2); }
  .bg img, .bg .plain { width: 100%; aspect-ratio: 16 / 10; object-fit: cover; border-radius: var(--r-sm); display: block; margin-bottom: 4px; }
  .bg .plain { background: var(--bg, var(--surface)); border: 1px dashed var(--border-strong); box-sizing: border-box; }
  .bg b { font-size: 14px; padding: 0 2px; }
  .bg small { font-size: 12px; color: var(--muted); padding: 0 2px; line-height: 1.3; }
  .bg.on { border-color: var(--accent); box-shadow: 0 0 0 2px color-mix(in srgb, var(--accent) 40%, transparent); }
  .nav { display: flex; gap: 8px; align-items: center; flex-wrap: wrap; }
  .grow { flex: 1; }
  @media (max-width: 520px) {
    .box { padding: 18px 16px 16px; }
    .body { min-height: 0; }
    .bgs { grid-template-columns: 1fr 1fr; }
  }
</style>
