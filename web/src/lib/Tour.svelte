<script>
  import { t } from './i18n.svelte.js';

  // The short tour: one part of the page after the other is lit up with a
  // few words next to it. Parts carry data-tour="…"; a part the page does not
  // show (hidden in the free layout, a shared dashboard …) is left out.
  // onAdd: opens the editor for a first tile (only offered on an empty dashboard).
  let { onDone, onAdd = null } = $props();

  const ALL = [
    { at: 'search', title: 'Suche', text: 'Finde Dienste, Filme, Serien und Dokumente. Mit / bist du sofort im Suchfeld; ✨ fragt die KI, wenn sie eingerichtet ist.' },
    { at: 'edit', title: 'Bearbeiten', text: 'Der Stift schaltet den Bearbeiten-Modus ein: Kacheln hinzufügen und verschieben, Gruppen anlegen – und mit ▦ Seite anordnen die ganze Seite frei gestalten, mit Uhr, Wetter und mehr.' },
    { at: 'start', title: 'Deine Dienste', text: 'Hier kommen deine Dienste hin. Wähle einen der Wege – oder später im Bearbeiten-Modus „+ Kachel“ in einer Gruppe.' },
    { at: 'group', title: 'Gruppen und Kacheln', text: 'Jede Gruppe sammelt Dienste als Kacheln. Im Bearbeiten-Modus fügt „+ Kachel“ einen Dienst hinzu: aussuchen, Adresse eintragen, mit API-Key gibt es Live-Daten. Der Punkt oben rechts zeigt, ob er läuft.' },
    { at: 'settings', title: 'Einstellungen', text: 'Aussehen (Themes, Hintergrund, Farben), Dashboards, Benachrichtigungen, Benutzer und Sicherung. Die Tour findest du jederzeit wieder unter Einstellungen → Info.' },
  ];
  const target = (s) => s.at && [...document.querySelectorAll(`[data-tour="${s.at}"]`)].find((el) => el.getClientRects().length);
  // Fixed when the tour starts: an empty dashboard shows its start card, not the groups.
  const steps = [...ALL.filter((s) => target(s)), { title: 'Das war’s!', text: 'Viel Spaß mit Heimdeck. Alles Weitere steht in der Anleitung – zu finden unter Einstellungen → Info.' }];

  let i = $state(0);
  let step = $derived(steps[i]);
  let rect = $state(null);
  let bubble = $state();
  let place = $state({ top: 0, left: 0, below: true });

  function measure() {
    const el = target(step);
    if (!el) {
      rect = null;
      return;
    }
    const r = el.getBoundingClientRect();
    rect = { top: r.top - 6, left: r.left - 6, width: r.width + 12, height: r.height + 12 };
    const bw = bubble?.offsetWidth || 320, bh = bubble?.offsetHeight || 160;
    const below = r.bottom + 14 + bh < innerHeight || r.top - 14 - bh < 0;
    place = {
      below,
      top: below ? Math.min(r.bottom + 14, innerHeight - bh - 8) : r.top - 14 - bh,
      left: Math.max(8, Math.min(r.left + r.width / 2 - bw / 2, innerWidth - bw - 8)),
    };
  }

  // Scroll the part into view, then light it up.
  $effect(() => {
    step;
    const el = target(step);
    if (el) {
      const r = el.getBoundingClientRect();
      if (r.top < 70 || r.bottom > innerHeight - 200) el.scrollIntoView({ block: r.height > innerHeight * 0.5 ? 'start' : 'center', behavior: 'instant' });
    }
    requestAnimationFrame(() => requestAnimationFrame(measure));
  });

  function key(e) {
    if (e.key === 'Escape') onDone();
    else if ((e.key === 'ArrowRight' || e.key === 'Enter') && i < steps.length - 1) { e.preventDefault(); i++; }
    else if (e.key === 'ArrowLeft' && i > 0) i--;
  }
</script>

<svelte:window onkeydown={key} onresize={measure} onscroll={measure} />

<div class="tour" role="dialog" aria-modal="true" aria-labelledby="tour-title">
  {#if rect}
    <div class="spot" style:top="{rect.top}px" style:left="{rect.left}px" style:width="{rect.width}px" style:height="{rect.height}px"></div>
  {:else}
    <div class="shade"></div>
  {/if}
  <div class="bubble card" class:center={!rect} bind:this={bubble}
    style:top={rect ? `${place.top}px` : null} style:left={rect ? `${place.left}px` : null}>
    <div class="count">{i + 1} / {steps.length}</div>
    <h3 id="tour-title">{t(step.title)}</h3>
    <p>{t(step.text)}</p>
    <div class="nav">
      {#if i < steps.length - 1}<button class="ghost" onclick={onDone}>{t('Beenden')}</button>{/if}
      <span class="grow"></span>
      {#if i > 0}<button onclick={() => i--}>←</button>{/if}
      {#if i < steps.length - 1}
        <button class="primary" onclick={() => i++}>{t('Weiter')} →</button>
      {:else}
        {#if onAdd}<button onclick={() => { onDone(); onAdd(); }}>+ {t('Ersten Dienst hinzufügen')}</button>{/if}
        <button class="primary" onclick={onDone}>{t('Fertig')}</button>
      {/if}
    </div>
  </div>
</div>

<style>
  .tour { position: fixed; inset: 0; z-index: 60; }
  /* The lit part: a hole in the shade, made by a huge shadow around it. */
  .spot { position: fixed; border-radius: calc(var(--radius) + 6px); box-shadow: 0 0 0 200vmax rgb(0 0 0 / .55), 0 0 0 2px var(--accent) inset; pointer-events: none; transition: top .25s, left .25s, width .25s, height .25s; }
  .shade { position: fixed; inset: 0; background: rgb(0 0 0 / .55); }
  .bubble { position: fixed; width: min(340px, calc(100vw - 16px)); padding: 16px 18px 14px; display: grid; gap: 8px; background: var(--surface-solid); box-shadow: var(--shadow); }
  .bubble.center { top: 50%; left: 50%; transform: translate(-50%, -50%); }
  .count { font-size: 12px; color: var(--muted); font-variant-numeric: tabular-nums; }
  h3 { margin: 0; font-size: 17px; }
  p { margin: 0; font-size: 14px; line-height: 1.5; color: var(--muted); }
  .nav { display: flex; gap: 6px; align-items: center; flex-wrap: wrap; margin-top: 4px; }
  .grow { flex: 1; }
  @media (prefers-reduced-motion: reduce) { .spot { transition: none; } }
</style>
