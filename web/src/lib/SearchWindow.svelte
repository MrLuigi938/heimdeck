<script>
  import { faviconUrl } from './api.js';
  import Icon from './Icon.svelte';
  import { tileHref, rewrite } from './network.svelte.js';
  import { t } from './i18n.svelte.js';
  import { confirmDialog } from './dialog.svelte.js';

  // The large search window: filters by kind of service, a list that works
  // with the arrow keys and a preview of the selected hit with its buttons.
  // The search itself (typing, asking the services, opening) lives in Search.svelte.
  let {
    q = $bindable(), parsed, tileHits, groups, loading, noHits, tileById, defs, local, newTab, recent, hasSearchServices,
    onInput, makeAction, webSearch, openUrl, onClose, onForget = null,
    // anchored: opens right below the search field in the header, which stays the input.
    anchored = false, input = null,
  } = $props();

  let field;
  let listEl;
  let backdrop;
  let cat = $state('all');
  // Nothing is selected until you use the arrow keys or point at a hit: Enter
  // alone searches the web, so a quick web search never opens a document instead.
  let sel = $state(-1);
  let sheet = $state(false); // phones: the preview slides up as a sheet
  let busy = $state('');

  const fieldEl = () => (anchored ? input : field);
  $effect(() => fieldEl()?.focus());

  // Anchored, the window sits right under the header's search field.
  let top = $state(0);
  const place = () => {
    if (anchored && input) top = Math.round(input.getBoundingClientRect().bottom + 8);
  };
  $effect(place);
  // Scrolling the page away from the field closes the anchored window.
  let scrolledFrom = scrollY;
  function onScroll() {
    if (!anchored) return;
    if (Math.abs(scrollY - scrolledFrom) > 40) onClose();
    else place();
  }
  let win;
  // Anchored there is no dark backdrop: a click anywhere else closes the window
  // (but not a click in a confirmation dialog opened from it).
  function onWindowDown(e) {
    if (anchored && win && !win.contains(e.target) && !input?.parentElement?.contains(e.target) && !e.target.closest?.('.backdrop:not(.anchored)')) onClose();
  }

  // Every hit in one list: tiles first, then the services in the server's order.
  let entries = $derived([
    ...tileHits.map((tile) => ({ key: 't' + tile.id, kind: 'tile', cat: 'tiles', section: 'tiles', tile })),
    ...groups.flatMap((g) =>
      (g.items || []).map((item, i) => {
        const tile = tileById[g.tileId];
        return { key: `g${g.tileId}-${i}`, kind: 'item', cat: defs[tile?.integration]?.category || t('Sonstiges'), section: g.tileId, group: g, item, tile };
      }),
    ),
  ]);
  // Filters only for what is there: "Kacheln" and the categories of the services with hits.
  let chips = $derived.by(() => {
    const count = {};
    for (const e of entries) count[e.cat] = (count[e.cat] || 0) + 1;
    const list = [{ id: 'all', label: t('Alle'), n: entries.length }];
    if (count.tiles) list.push({ id: 'tiles', label: t('Kacheln'), n: count.tiles });
    for (const c of Object.keys(count).filter((c) => c !== 'tiles')) list.push({ id: c, label: c, n: count[c] });
    return list;
  });
  let visible = $derived(cat === 'all' ? entries : entries.filter((e) => e.cat === cat));
  let current = $derived(sel >= 0 ? visible[Math.min(sel, visible.length - 1)] : null);

  $effect(() => {
    // A new search or filter starts at the top; a filter without hits falls back to all.
    q;
    cat;
    sel = -1;
  });
  $effect(() => {
    if (cat !== 'all' && !chips.some((c) => c.id === cat)) cat = 'all';
  });

  const sectionName = (e) => (e.kind === 'tile' ? t('Kacheln') : e.group.name);
  const link = (e, u) => rewrite(u, e.tile, local);

  function activate(e) {
    if (!e) return webSearch();
    if (e.kind === 'tile') openUrl(tileHref(e.tile, local));
    else if (e.item.url) openUrl(link(e, e.item.url));
    else sheet = true; // nothing to open: show what can be done with it
  }

  function move(d) {
    if (!visible.length) return;
    sel = sel < 0 ? (d > 0 ? 0 : visible.length - 1) : (Math.min(sel, visible.length - 1) + d + visible.length) % visible.length;
    queueMicrotask(() => listEl?.querySelector('.entry.sel')?.scrollIntoView({ block: 'nearest' }));
  }

  // The header's field forwards its keys here when the window is anchored.
  export function handleKey(e) {
    onKey(e);
  }

  function onKey(e) {
    if (e.key === 'ArrowDown') { e.preventDefault(); move(1); }
    else if (e.key === 'ArrowUp') { e.preventDefault(); move(-1); }
    else if (e.key === 'Enter') { e.preventDefault(); if (q.trim()) activate(current); }
    else if (e.key === 'Tab' && chips.length > 1) {
      e.preventDefault();
      const i = chips.findIndex((c) => c.id === cat);
      cat = chips[(i + (e.shiftKey ? -1 : 1) + chips.length) % chips.length].id;
    } else if (e.key === 'Escape') {
      e.preventDefault();
      if (sheet) sheet = false;
      else onClose();
    }
  }
  // Esc also works when the focus left the field (e.g. after clicking a filter).
  function onWindowKey(e) {
    if (e.key !== 'Escape' || e.defaultPrevented) return;
    const all = document.querySelectorAll('.backdrop');
    if (all[all.length - 1] !== backdrop) return;
    e.preventDefault();
    if (sheet) sheet = false;
    else onClose();
  }

  const phone = () => matchMedia('(max-width: 640px)').matches;
  function pick(e, i) {
    sel = i;
    if (phone()) sheet = true;
    else activate(e);
  }

  async function run(e, a) {
    if (a.confirm && !(await confirmDialog(a.confirm, { confirmLabel: a.label, danger: a.tone === 'danger' }))) return;
    busy = a.id + JSON.stringify(a.params || {});
    try {
      await makeAction(e.group.tileId)(a);
    } finally {
      busy = '';
    }
  }

  function useRecent(term) {
    q = term;
    onInput();
    fieldEl()?.focus();
  }

  const host = (u) => {
    try {
      return new URL(u).host;
    } catch {
      return '';
    }
  };
</script>

<svelte:window onkeydown={onWindowKey} onmousedown={onWindowDown} onscroll={onScroll} onresize={place} />

<!-- class "backdrop": the dashboard's shortcuts pause while it is open. -->
<div class="backdrop" class:anchored role="presentation" bind:this={backdrop} style:padding-top={anchored ? top + 'px' : null}
  onmousedown={(e) => !anchored && e.target === backdrop && onClose()}>
  <div class="win card" bind:this={win} role="dialog" aria-modal={!anchored} aria-label={t('Suche')}>
    {#if !anchored}
    <div class="top">
      <svg class="glass" viewBox="0 0 24 24" width="20" height="20" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><circle cx="11" cy="11" r="7" /><path d="m20 20-3.5-3.5" /></svg>
      <input bind:this={field} bind:value={q} oninput={onInput} onkeydown={onKey} autocomplete="off" aria-label={t('Suche')}
        placeholder={hasSearchServices ? t('Suchen in Diensten, Filmen, Serien, Dokumenten …') : t('Dienste suchen oder im Web suchen …')} />
      {#if q}<button class="clear" onclick={() => { q = ''; onInput(); field.focus(); }} aria-label={t('Suche leeren')}>✕</button>{/if}
      <button class="ghost close" onclick={onClose} aria-label={t('Schließen')}><kbd>Esc</kbd><span class="close-text">{t('Schließen')}</span></button>
    </div>
    {/if}

    {#if q.trim()}
      <div class="chips" role="tablist">
        {#each chips as c (c.id)}
          <button class="chip" class:on={cat === c.id} role="tab" aria-selected={cat === c.id} onclick={() => { cat = c.id; fieldEl()?.focus(); }}>
            {c.label} <span class="n">{c.n}</span>
          </button>
        {/each}
        {#if loading}<span class="loading">{t('Suche in deinen Diensten …')}</span>{/if}
      </div>

      <div class="body">
        <div class="list" bind:this={listEl}>
          {#each visible as e, i (e.key)}
            {#if i === 0 || visible[i - 1].section !== e.section}
              <div class="sec">
                {#if e.kind === 'item'}<Icon icon={e.group.icon} name={e.group.name} favicon={faviconUrl(e.tile)} size={14} />{/if}
                {sectionName(e)}
              </div>
            {/if}
            <button class="entry" class:sel={current === e} onmousemove={() => (sel = i)} onclick={() => pick(e, i)}>
              {#if e.kind === 'tile'}
                <Icon icon={e.tile.icon} name={e.tile.name} favicon={faviconUrl(e.tile)} size={26} />
                <span class="txt"><b>{e.tile.name}</b>{#if e.tile.description}<small>{e.tile.description}</small>{/if}</span>
              {:else}
                {#if e.item.image}<img class="thumb" src={link(e, e.item.image)} alt="" loading="lazy" onerror={(ev) => (ev.currentTarget.style.visibility = 'hidden')} />{/if}
                <span class="txt"><b>{e.item.title}</b>{#if e.item.subtitle}<small>{e.item.subtitle}</small>{/if}</span>
                {#if e.item.badge}<span class="badge">{e.item.badge}</span>{/if}
              {/if}
            </button>
          {/each}
          {#if !visible.length && !loading}
            <p class="empty">{t('Keine Treffer.')}</p>
          {/if}
          {#if !loading && noHits.length}
            <p class="none">{t('Keine Treffer in')}: {noHits.join(', ')}</p>
          {/if}
          <button class="entry web" class:sel={!current} onclick={webSearch}>🌐 {t('Im Web suchen: „{q}“', { q: parsed.term || q.trim() })}</button>
        </div>

        <div class="preview" class:open={sheet}>
          {#if current}
            <button class="ghost sheet-close" onclick={() => (sheet = false)} aria-label={t('Schließen')}>✕</button>
            {#if current.kind === 'tile'}
              <div class="p-icon"><Icon icon={current.tile.icon} name={current.tile.name} favicon={faviconUrl(current.tile)} size={64} /></div>
              <h3>{current.tile.name}</h3>
              {#if current.tile.description}<p class="muted">{current.tile.description}</p>{/if}
              {#if host(tileHref(current.tile, local))}<p class="muted small">{host(tileHref(current.tile, local))}</p>{/if}
              <div class="p-actions"><button class="primary" onclick={() => activate(current)}>{t('Öffnen')}</button></div>
            {:else}
              {#if current.item.image}<img class="p-image" src={link(current, current.item.image)} alt="" onerror={(ev) => (ev.currentTarget.style.display = 'none')} />{/if}
              <h3>{current.item.title}</h3>
              {#if current.item.badge}<span class="badge">{current.item.badge}</span>{/if}
              {#if current.item.subtitle}<p class="muted">{current.item.subtitle}</p>{/if}
              {#if current.item.progress != null}<div class="bar"><div style="width:{current.item.progress}%"></div></div>{/if}
              <div class="p-actions">
                {#each current.item.actions || [] as a}
                  <button class={a.tone || ''} disabled={busy === a.id + JSON.stringify(a.params || {})} onclick={() => run(current, a)}>{a.label}</button>
                {/each}
                {#if current.item.url}
                  <button class:primary={!current.item.actions?.length} onclick={() => activate(current)}>{t('In {service} öffnen', { service: current.group.name })}</button>
                {/if}
              </div>
              <p class="muted small from"><Icon icon={current.group.icon} name={current.group.name} favicon={faviconUrl(current.tile)} size={14} /> {current.group.name}</p>
            {/if}
          {:else}
            <div class="hint">
              <p>🌐 {t('Enter sucht im Web nach „{q}“.', { q: parsed.term || q.trim() })}</p>
              <p class="muted small">{t('Mit ↑ ↓ oder der Maus einen Treffer auswählen – dann öffnet Enter ihn.')}</p>
            </div>
          {/if}
        </div>
      </div>
      <div class="keys">
        <span><kbd>↑</kbd> <kbd>↓</kbd> {t('auswählen')}</span>
        <span><kbd>Enter</kbd> {t('öffnen, ohne Auswahl im Web suchen')}</span>
        {#if chips.length > 1}<span><kbd>Tab</kbd> {t('nächster Filter')}</span>{/if}
        <span><kbd>!name</kbd> {t('nur in einem Dienst suchen')}</span>
      </div>
    {:else}
      <div class="start">
        {#if recent.length}
          <div class="sec">{t('Zuletzt gesucht')}</div>
          <div class="recent">
            {#each recent as r (r)}
              <span class="rchip"><button class="chip" onclick={() => useRecent(r)}>{r}</button>{#if onForget}<button class="forget" onclick={() => onForget(r)} aria-label={t('„{q}“ aus dem Verlauf entfernen', { q: r })} title={t('Aus dem Verlauf entfernen')}>✕</button>{/if}</span>
            {/each}
          </div>
        {/if}
        <p class="muted small">
          {hasSearchServices
            ? t('Findet deine Kacheln sofort und fragt alle Dienste, die suchen können. Mit !name am Anfang nur in einem Dienst suchen, z. B. „!jelly severance“.')
            : t('Findet deine Kacheln; Enter sucht im Web.')}
        </p>
      </div>
    {/if}
  </div>
</div>

<style>
  .backdrop {
    position: fixed; inset: 0; z-index: 60; background: rgba(0, 0, 0, .55);
    display: grid; justify-items: center; align-items: start; padding: 7vh 16px 16px; animation: fade .12s;
  }
  /* Anchored: no dark layer; clicks go through to the page (and close the window). */
  .backdrop.anchored { background: none; pointer-events: none; animation: none; }
  .backdrop.anchored .win { pointer-events: auto; box-shadow: 0 18px 50px rgba(0, 0, 0, .45); max-height: min(640px, calc(100dvh - 120px)); }
  .win {
    width: min(980px, 100%); max-height: min(680px, 86vh); max-height: min(680px, 86dvh);
    display: flex; flex-direction: column; background: var(--surface-solid); overflow: hidden;
  }
  .top { display: flex; align-items: center; gap: 10px; padding: 12px 14px; border-bottom: 1px solid var(--border); }
  .glass { color: var(--muted); flex: none; }
  .top input { border: 0; background: none; box-shadow: none; padding: 6px 0; font-size: 18px; flex: 1; min-width: 0; }
  .top input:focus { box-shadow: none; }
  .clear { border: 0; background: var(--surface-2); width: 26px; height: 26px; padding: 0; border-radius: 50%; color: var(--muted); flex: none; }
  .close { padding: 4px; flex: none; }
  kbd { font-family: ui-monospace, monospace; font-size: 11px; border: 1px solid var(--border-strong); border-radius: 4px; padding: 0 5px; color: var(--muted); }
  .chips { display: flex; gap: 6px; padding: 10px 14px; border-bottom: 1px solid var(--border); overflow-x: auto; align-items: center; scrollbar-width: none; }
  .chip { border-radius: 999px; padding: 4px 12px; font-size: 13px; white-space: nowrap; background: var(--surface-2); border: 1px solid var(--border); }
  .chip.on { background: color-mix(in srgb, var(--accent) 18%, transparent); border-color: var(--accent); color: var(--accent); }
  .n { color: var(--muted); font-size: 12px; margin-left: 2px; }
  .chip.on .n { color: inherit; }
  .loading { font-size: 12.5px; color: var(--muted); white-space: nowrap; }
  .body { display: grid; grid-template-columns: minmax(0, 1.15fr) minmax(0, 1fr); min-height: 0; flex: 1; }
  .list { overflow-y: auto; padding: 6px 8px 10px; border-right: 1px solid var(--border); display: grid; align-content: start; gap: 1px; overscroll-behavior: contain; }
  .sec { display: flex; gap: 6px; align-items: center; font-size: 11.5px; text-transform: uppercase; letter-spacing: .06em; color: var(--muted); padding: 10px 8px 4px; }
  .entry { display: flex; align-items: center; gap: 10px; width: 100%; text-align: left; border: 0; background: none; padding: 7px 8px; border-radius: var(--r-sm); min-width: 0; }
  .entry.sel { background: color-mix(in srgb, var(--accent) 14%, transparent); }
  .txt { display: grid; min-width: 0; flex: 1; }
  .txt b { font-weight: 500; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .txt small { font-size: 12px; color: var(--muted); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .thumb { width: 30px; height: 44px; object-fit: cover; border-radius: 4px; flex: none; }
  .badge { font-size: 10.5px; padding: 1px 6px; border-radius: 6px; background: var(--surface-2); color: var(--muted); white-space: nowrap; flex: none; }
  .web { color: var(--muted); margin-top: 6px; border-top: 1px solid var(--border); border-radius: 0; padding-top: 10px; }
  .empty, .none { font-size: 13px; color: var(--muted); padding: 8px; margin: 0; }
  .preview { overflow-y: auto; padding: 16px; display: flex; flex-direction: column; gap: 8px; align-items: flex-start; }
  .preview h3 { margin: 4px 0 0; font-size: 18px; }
  .preview p { margin: 0; }
  .p-icon { padding: 8px 0; }
  .p-image { width: 100%; max-height: 260px; object-fit: contain; object-position: left; border-radius: var(--r-sm); }
  .p-actions { display: flex; flex-wrap: wrap; gap: 6px; margin-top: 6px; }
  .bar { height: 5px; width: 100%; background: var(--surface-2); border-radius: 4px; overflow: hidden; }
  .bar > div { height: 100%; background: var(--accent); }
  .small { font-size: 12.5px; }
  .from { display: flex; gap: 6px; align-items: center; margin-top: auto !important; padding-top: 10px; }
  .sheet-close, .close-text { display: none; }
  .keys { display: flex; gap: 16px; flex-wrap: wrap; padding: 8px 14px; border-top: 1px solid var(--border); font-size: 12px; color: var(--muted); }
  .start { padding: 10px 14px 16px; display: grid; gap: 8px; }
  .start .sec { padding: 4px 0 0; }
  .recent { display: flex; gap: 6px; flex-wrap: wrap; }
  .rchip { display: inline-flex; align-items: center; }
  .rchip .chip { border-top-right-radius: 0; border-bottom-right-radius: 0; border-right: 0; }
  .forget { border-radius: 0 999px 999px 0; padding: 4px 9px 4px 6px; font-size: 11px; color: var(--muted); background: var(--surface-2); border: 1px solid var(--border); }
  .forget:hover { color: var(--bad); }
  .hint { display: grid; gap: 6px; padding: 8px 2px; }
  .hint p { margin: 0; }
  @keyframes fade { from { opacity: 0; } }

  /* Phones: full screen, only the list; a tap opens the preview as a sheet from below. */
  @media (max-width: 640px) {
    .backdrop { padding: 0; }
    .win { width: 100%; height: 100dvh; max-height: none; border-radius: 0; }
    .body { grid-template-columns: minmax(0, 1fr); position: relative; }
    .list { border-right: 0; }
    .preview {
      position: absolute; inset: auto 0 0 0; max-height: 75%; background: var(--surface-solid);
      border-top: 1px solid var(--border-strong); border-radius: 16px 16px 0 0; box-shadow: 0 -10px 30px rgba(0, 0, 0, .35);
      transform: translateY(105%); transition: transform .2s; z-index: 2;
    }
    .preview.open { transform: none; }
    .sheet-close { display: inline-grid; align-self: flex-end; }
    .p-image { max-height: 170px; }
    .keys, .close kbd { display: none; }
    .close-text { display: inline; font-size: 14px; color: var(--accent); }
  }
</style>
