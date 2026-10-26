<script module>
  // A freely arranged group: 12 columns that grow with the window, rows of a
  // fixed height. Two rows plus the gap (70 px) fit a plain tile.
  export const COLS = 12;
  const ROW = 29;
  const GAP = 12;
  const MIN_H = 2;

  const overlaps = (a, b) => a.x < b.x + b.w && b.x < a.x + a.w && a.y < b.y + b.h && b.y < a.y + a.h;
  const clamp = (v, lo, hi) => Math.min(hi, Math.max(lo, v));

  // Puts `moving` in place and pushes the tiles it covers further down. Tiles
  // never move up on their own, so gaps the user left stay where they are.
  function settle(items, moving) {
    const placed = [moving];
    const rest = items.filter((i) => i.id !== moving.id).map((i) => ({ ...i })).sort((a, b) => a.y - b.y || a.x - b.x);
    for (const it of rest) {
      let hit;
      while ((hit = placed.filter((p) => overlaps(p, it))).length) it.y = Math.max(...hit.map((p) => p.y + p.h));
      placed.push(it);
    }
    return placed;
  }

  // First free spot for a w×h tile, row by row from the top left.
  function freeSpot(items, w, h) {
    for (let y = 0; ; y++) {
      for (let x = 0; x + w <= COLS; x++) {
        const c = { x, y, w, h };
        if (!items.some((i) => overlaps(i, c))) return c;
      }
    }
  }

  // Tiles without a position yet (new, moved here, or the group was just
  // switched to the free grid) get the first free spot after the placed ones.
  function arrange(tiles, size) {
    const items = tiles.filter((t) => t.grid).map((t) => ({ id: t.id, ...t.grid }));
    for (const t of tiles) {
      if (t.grid) continue;
      const s = size(t);
      items.push({ id: t.id, ...freeSpot(items, s.w, s.h) });
    }
    return items;
  }

  const positions = (list) => Object.fromEntries(list.map(({ id, x, y, w, h }) => [id, { x, y, w, h }]));

  // Reading order (top to bottom, left to right), used on phones and for the saved order.
  export function byPosition(tiles) {
    const pos = (t) => (t.grid ? t.grid.y * COLS + t.grid.x : Infinity);
    return [...tiles].sort((a, b) => pos(a) - pos(b));
  }
</script>

<script>
  import Tile from './Tile.svelte';
  import { t } from './i18n.svelte.js';

  // cols below COLS (phones) shows the tiles flowing, see below: they can still be
  // opened and edited there, but not moved or resized.
  let { tiles, defs, status, editMode, newTab, local, cols = COLS, dragging = null, findTile, grab, tileDragProps, onEdit, onCommit } = $props();
  let arranging = $derived(editMode && cols === COLS);

  // Starting size, close to what the tile takes in rows.
  function size(tile) {
    const def = defs[tile.integration];
    if (def?.hasWidget && tile.showWidget) return { w: 4, h: 6 };
    return { w: 2, h: 2 };
  }

  let items = $derived(arrange(tiles, size));
  let preview = $state(null); // positions while a tile is dragged or resized
  let resizing = $state(null);
  let shown = $derived(preview || items);

  // Phones: no fixed rows. Tiles follow the desktop arrangement in reading
  // order, take the height their content needs, live tiles get the whole
  // width (those of 2–3 desktop columns half of it) and plain links become icons
  // that fill rows together. The desktop arrangement itself is not changed.
  let flow = $derived(cols < COLS);
  let ordered = $derived.by(() => {
    if (!flow) return tiles;
    const at = Object.fromEntries(items.map((i) => [i.id, i]));
    return [...tiles].sort((a, b) => at[a.id].y - at[b.id].y || at[a.id].x - at[b.id].x);
  });
  function phoneKind(tile) {
    const it = items.find((i) => i.id === tile.id);
    const live = defs[tile.integration]?.hasWidget && tile.showWidget;
    if (!live || it.w === 1) return 'icon';
    return it.w >= 4 ? 'full' : 'half';
  }
  let byId = $derived(Object.fromEntries(shown.map((i) => [i.id, i])));
  // While arranging, a few empty rows below make room to drop tiles further down.
  let rows = $derived(Math.max(1, ...shown.map((i) => i.y + i.h)) + (arranging ? 3 : 0));
  // A tile dragged in from another group: shown as an outline where it lands.
  let incoming = $derived(dragging != null && preview && !tiles.some((tl) => tl.id === dragging) ? byId[dragging] : null);
  let gridEl;
  let last = '';

  $effect(() => {
    if (dragging == null && !resizing) {
      preview = null;
      last = '';
    }
  });

  function metrics() {
    const r = gridEl.getBoundingClientRect();
    return { r, colStep: (r.width - GAP * (COLS - 1)) / COLS + GAP, rowStep: ROW + GAP };
  }

  function dragover(e) {
    if (!arranging || dragging == null) return;
    e.preventDefault();
    e.stopPropagation();
    e.dataTransfer.dropEffect = 'move';
    let s = items.find((i) => i.id === dragging);
    if (!s) {
      const tile = findTile(dragging);
      if (!tile) return;
      s = size(tile);
    }
    const { r, colStep, rowStep } = metrics();
    const g = grab();
    const x = clamp(Math.round((e.clientX - g.x - r.left) / colStep), 0, COLS - s.w);
    const y = Math.max(0, Math.round((e.clientY - g.y - r.top) / rowStep));
    if (preview && last === `${x},${y}`) return;
    last = `${x},${y}`;
    preview = settle(items, { id: dragging, x, y, w: s.w, h: s.h });
  }

  function dragleave(e) {
    if (!gridEl.contains(e.relatedTarget)) {
      preview = null;
      last = '';
    }
  }

  function drop(e) {
    if (!arranging || dragging == null) return;
    e.preventDefault();
    e.stopPropagation();
    if (preview) onCommit(positions(preview), dragging);
    preview = null;
    last = '';
  }

  // ---- resizing with the corner handle ----
  function startResize(e, it) {
    if (e.button !== 0) return;
    e.preventDefault();
    e.stopPropagation();
    e.currentTarget.setPointerCapture(e.pointerId);
    resizing = { it, x0: e.clientX, y0: e.clientY };
  }

  function moveResize(e) {
    if (!resizing) return;
    const { colStep, rowStep } = metrics();
    const { it } = resizing;
    const w = clamp(it.w + Math.round((e.clientX - resizing.x0) / colStep), 1, COLS - it.x);
    const h = Math.max(MIN_H, it.h + Math.round((e.clientY - resizing.y0) / rowStep));
    const now = byId[it.id];
    if (preview && now.w === w && now.h === h) return;
    preview = settle(items, { ...it, w, h });
  }

  function endResize() {
    if (!resizing) return;
    const result = preview;
    resizing = null;
    preview = null;
    if (result) onCommit(positions(result));
  }

  function cancelResize() {
    resizing = null;
    preview = null;
  }

  function keyResize(e, it) {
    const d = { ArrowRight: [1, 0], ArrowLeft: [-1, 0], ArrowDown: [0, 1], ArrowUp: [0, -1] }[e.key];
    if (!d) return;
    e.preventDefault();
    const w = clamp(it.w + d[0], 1, COLS - it.x);
    const h = Math.max(MIN_H, it.h + d[1]);
    if (w !== it.w || h !== it.h) onCommit(positions(settle(items, { ...it, w, h })));
  }

  // Double-click on the handle: height to fit the content.
  function fitHeight(e, it) {
    const el = e.currentTarget.parentElement.querySelector(':scope > .tile');
    if (!el) return;
    el.style.height = 'auto';
    const natural = el.getBoundingClientRect().height;
    el.style.height = '';
    const h = Math.max(MIN_H, Math.ceil((natural + GAP) / (ROW + GAP)));
    if (h !== it.h) onCommit(positions(settle(items, { ...it, h })));
  }

  const area = (it) => `${it.y + 1} / ${it.x + 1} / span ${it.h} / span ${it.w}`;

  // Marks a tile whose content is taller than the tile ("scrolls"). Only then
  // does the mouse wheel stay inside it; otherwise the page scrolls as usual.
  // Checked again whenever the tile or its content changes size.
  function overflowWatch(cell) {
    const tile = () => cell.querySelector(':scope > .tile');
    const check = () => {
      const el = tile();
      if (el) el.classList.toggle('scrolls', el.scrollHeight > el.clientHeight + 1);
    };
    const ro = new ResizeObserver(check);
    const watch = () => {
      ro.disconnect();
      const el = tile();
      if (!el) return;
      ro.observe(el);
      for (const child of el.children) ro.observe(child);
      check();
    };
    const mo = new MutationObserver(watch);
    mo.observe(cell, { childList: true, subtree: true });
    watch();
    return { destroy: () => { ro.disconnect(); mo.disconnect(); } };
  }
</script>

{#if flow}
<div class="flow" role="list">
  {#each ordered as tile (tile.id)}
    {@const kind = phoneKind(tile)}
    <!-- Long lists fold here ("show more"), so a tile does not get endlessly tall. -->
    <div class="fcell {kind}" role="listitem">
      <Tile {tile} def={defs[tile.integration]} status={status[tile.id]} {editMode} {newTab} {local} compact={kind === 'icon' ? 'icons' : ''}
        onEdit={onEdit && (() => onEdit(tile))} />
    </div>
  {/each}
</div>
{:else}
<div class="free" class:editing={arranging} bind:this={gridEl} style:--rows={rows} style:--cols={cols}
  role="list" ondragover={dragover} ondragleave={dragleave} ondrop={drop}>
  {#if arranging}
    <div class="cells" aria-hidden="true">
      {#each { length: rows * COLS }, i (i)}<span></span>{/each}
    </div>
  {/if}
  {#each tiles as tile (tile.id)}
    {@const it = byId[tile.id]}
    {#if it}
      <div class="cell" role="listitem" use:overflowWatch class:moving={(dragging === tile.id && preview) || resizing?.it.id === tile.id} style:grid-area={area(it)}>
        <!-- One column wide is too narrow for name and live data: the tile becomes an icon. -->
        <Tile {tile} def={defs[tile.integration]} status={status[tile.id]} {editMode} {newTab} {local} compact={it.w === 1 ? 'icons' : ''} free
          onEdit={onEdit && (() => onEdit(tile))} dragProps={arranging ? tileDragProps(tile) : {}} />
        {#if arranging}
          <button class="resize" title={t('Größe ändern')} aria-label={t('Größe ändern')}
            onpointerdown={(e) => startResize(e, it)} onpointermove={moveResize} onpointerup={endResize} onpointercancel={cancelResize}
            ondblclick={(e) => fitHeight(e, it)} onkeydown={(e) => keyResize(e, it)}></button>
        {/if}
      </div>
    {/if}
  {/each}
  {#if incoming}
    <div class="landing" style:grid-area={area(incoming)}></div>
  {/if}
</div>
{/if}

<style>
  .free {
    position: relative; display: grid; gap: 12px;
    grid-template-columns: repeat(var(--cols), minmax(0, 1fr)); grid-template-rows: repeat(var(--rows), 29px);
  }
  .cells {
    position: absolute; inset: 0; display: grid; gap: 12px; pointer-events: none;
    grid-template-columns: repeat(12, minmax(0, 1fr)); grid-template-rows: repeat(var(--rows), 29px);
  }
  .cells span { border: 1px dashed var(--border-strong); border-radius: var(--r-xs); opacity: .45; }
  /* Each cell is a size container, so icons and names can follow its size. */
  .cell { position: relative; min-width: 0; min-height: 0; container-type: size; --icon-max: 42cqmin; }
  /* The grid sets the size; content that does not fit scrolls inside the tile.
     The scrollbar only shows while the pointer is on the tile. */
  .cell > :global(.tile) {
    height: 100%; overflow: auto;
    scrollbar-width: thin; scrollbar-color: transparent transparent;
  }
  .cell > :global(.tile.scrolls) { overscroll-behavior: contain; }
  .cell > :global(.tile:hover) { scrollbar-color: var(--border-strong) transparent; }
  /* Icon tiles scale with the cell instead of scrolling. */
  .cell > :global(.tile.compact) { aspect-ratio: auto; overflow: hidden; }
  .cell :global(.tile.compact .square) { padding: 6px; gap: 4px; }
  @container (max-height: 84px) {
    .cell :global(.tile.compact .cname) { display: none; }
  }
  .cell.moving > :global(.tile) { opacity: .45; }
  .landing { border: 2px dashed var(--accent); border-radius: var(--radius); background: color-mix(in srgb, var(--accent) 10%, transparent); }
  .resize {
    position: absolute; right: 0; bottom: 0; z-index: 2; width: 22px; height: 22px; padding: 0;
    border: 0; background: none; cursor: nwse-resize; touch-action: none;
  }
  .resize::after {
    content: ''; position: absolute; right: 5px; bottom: 5px; width: 8px; height: 8px;
    border-right: 2px solid var(--muted); border-bottom: 2px solid var(--muted); border-radius: 0 0 3px 0;
  }
  .resize:hover::after, .resize:focus-visible::after { border-color: var(--accent); }

  /* Phones: four columns, rows as high as their content. "dense" lets icons
     fill the gap next to a half-width tile. Half-width tiles side by side
     share the height of the taller one; icons stay square. */
  .flow { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 10px; grid-auto-flow: row dense; align-items: start; }
  .fcell { min-width: 0; }
  .fcell.full { grid-column: 1 / -1; }
  .fcell.half { grid-column: span 2; align-self: stretch; }
  .fcell.half > :global(.tile) { height: 100%; }
  /* A quarter of a phone is too small for two lines under the icon: one line,
     ending in "…" (the full name is in the tooltip). */
  .fcell.icon :global(.cname) { display: block; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; -webkit-line-clamp: unset; line-clamp: unset; }
</style>
