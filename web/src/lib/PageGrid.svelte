<script module>
  import { COLS, overlaps, settle, freeSpot } from './FreeGrid.svelte';

  // The whole page as a grid of blocks: title, search, buttons and groups.
  // 12 columns that grow with the window. While arranging, rows have a fixed
  // height, so the boxes show exactly where things go; otherwise a row grows
  // with its content (a group with more tiles pushes what is below further down).
  export const ROW = 29;
  export const GAP = 12;
  const clamp = (v, lo, hi) => Math.min(hi, Math.max(lo, v));

  // Blocks without a position (new groups, or the layout was just started) go
  // to the first free spot after the placed ones.
  // A stored place smaller than the block's minimum (e.g. the search one row
  // high, where its field does not fit) grows to it; whatever that overlaps
  // is pushed down, so no block ever lies over another one.
  // A block's smallest size at a width: min, and for some (a text) minH(w),
  // the rows its content needs at that width to stay readable.
  // colPx: how wide one column is right now, in pixels.
  export function minSize(b, w, colPx = 100) {
    const m = b?.min || { w: 1, h: 1 };
    return b?.minH ? { w: m.w, h: Math.max(m.h, b.minH(Math.max(m.w, w), colPx)) } : m;
  }

  export function placeBlocks(blocks, positions, colPx = 100) {
    const items = [];
    let grown = false;
    for (const b of blocks) {
      const p = positions?.[b.id];
      if (!p) continue;
      const w = Math.min(COLS, Math.max(minSize(b, p.w, colPx).w, p.w));
      const h = Math.max(minSize(b, w, colPx).h, p.h);
      grown ||= w !== p.w || h !== p.h;
      items.push({ id: b.id, ...p, x: Math.min(p.x, COLS - w), w, h });
    }
    const placed = grown ? pack(items) : items;
    for (const b of blocks) {
      if (positions?.[b.id]) continue;
      const s = b.size || { w: 3, h: 2 };
      placed.push({ id: b.id, ...freeSpot(placed, Math.min(s.w, COLS), s.h) });
    }
    return placed;
  }

  // Pushes blocks down until none overlap, top to bottom.
  export function pack(items) {
    const placed = [];
    for (const it of [...items].sort((a, b) => a.y - b.y || a.x - b.x).map((i) => ({ ...i }))) {
      let hit;
      while ((hit = placed.filter((p) => overlaps(p, it))).length) it.y = Math.max(...hit.map((p) => p.y + p.h));
      placed.push(it);
    }
    return placed;
  }

  // fix: the height was set by hand (groups only) – kept, the content scrolls inside.
  export const toPositions = (list) => Object.fromEntries(list.map(({ id, x, y, w, h, fix }) => [id, fix ? { x, y, w, h, fix } : { x, y, w, h }]));

  // Reading order of the blocks: top to bottom, left to right.
  export function readingOrder(positions) {
    return (a, b) => {
      const pa = positions?.[a] || { x: 0, y: 1e6 };
      const pb = positions?.[b] || { x: 0, y: 1e6 };
      return pa.y - pb.y || pa.x - pb.x;
    };
  }
</script>

<script>
  import { tick } from 'svelte';
  import { t } from './i18n.svelte.js';

  // blocks: [{ id, label, size: {w, h}, min: {w, h}, fixed }]; fixed blocks keep
  // their height and scroll inside (the search panel), the others may grow.
  // block: snippet (block, {w, h}) that draws the content.
  // tools: optional snippet (block) with small buttons on the block while arranging (settings, hide, remove).
  let { blocks, positions = {}, arranging = false, onChange, block, tools = null } = $props();

  // The width of one column, for blocks whose smallest height depends on it (a text).
  let colPx = $state(100);
  let items = $derived(placeBlocks(blocks, positions, colPx));
  let preview = $state(null);
  let shown = $derived(preview || items);
  let byId = $derived(Object.fromEntries(shown.map((i) => [i.id, i])));
  let rows = $derived(Math.max(1, ...shown.map((i) => i.y + i.h)) + (arranging ? 4 : 0));
  let gridEl;
  let cells = {};
  let action = $state(null); // { kind: 'move' | 'size', it, x0, y0 }

  // w: the width the block has (or gets) – some blocks need more rows when narrower.
  const minOf = (id, w = byId[id]?.w || 1) => minSize(blocks.find((b) => b.id === id), w, colPx);
  $effect(() => {
    if (!gridEl) return;
    const ro = new ResizeObserver(() => {
      const c = Math.round((gridEl.clientWidth - GAP * (COLS - 1)) / COLS);
      if (c > 0 && c !== colPx) colPx = c;
    });
    ro.observe(gridEl);
    return () => ro.disconnect();
  });
  // A group grows with its tiles (live data loading, lists getting longer), so
  // nothing is ever cut off. Only a height set by hand stays: then the tiles
  // scroll inside the group.
  const isFixed = (b) => b.fixed || (b.kind === 'group' && (arranging || !!positions?.[b.id]?.fix));

  // Rows a block's content needs at its current width.
  function needRows(id) {
    const el = cells[id]?.firstElementChild;
    if (!el) return 0;
    return Math.ceil((el.scrollHeight + GAP) / (ROW + GAP));
  }
  // Blocks whose content is taller than the block (marked while arranging).
  let tooSmall = $state({});
  $effect(() => {
    shown;
    if (!arranging) return;
    const frame = requestAnimationFrame(() => {
      const out = {};
      for (const b of blocks) if (b.kind === 'group' && byId[b.id]?.fix && needRows(b.id) > byId[b.id].h) out[b.id] = true;
      tooSmall = out;
    });
    return () => cancelAnimationFrame(frame);
  });
  // Height to fit the content, growing with it again (double-click on the
  // corner, or after a group got narrower or wider).
  function fit(id) {
    const it = items.find((i) => i.id === id);
    const h = Math.max(minOf(id).h, needRows(id));
    if (it && h && (h !== it.h || it.fix)) onChange(toPositions(settle(items, { ...it, h, fix: false })));
  }

  function steps() {
    const r = gridEl.getBoundingClientRect();
    return { colStep: (r.width - GAP * (COLS - 1)) / COLS + GAP, rowStep: ROW + GAP };
  }

  // Heights the content needs right now, in rows: called before arranging
  // starts, so the fixed rows do not cut anything off.
  // fresh: the layout was just started, groups take the height their tiles need.
  export function fitted() {
    const out = items.map((it) => {
      const el = cells[it.id];
      if (!el) return it;
      // Groups: a hand-set height stays, the others take what their tiles need.
      if (it.id.startsWith('g')) return it.fix ? it : { ...it, h: Math.max(minOf(it.id).h, needRows(it.id)) };
      // The others: what their content needs (not the cell, which a tall neighbour
      // in the same rows may have stretched); the search panel keeps its height.
      if (blocks.find((x) => x.id === it.id)?.fixed) return it;
      return { ...it, h: Math.max(it.h, needRows(it.id)) };
    });
    return toPositions(pack(out));
  }

  function start(e, it, kind) {
    if (e.button !== 0 || !arranging) return;
    e.preventDefault();
    e.stopPropagation();
    e.currentTarget.setPointerCapture(e.pointerId);
    action = { kind, it, x0: e.clientX, y0: e.clientY };
  }

  function move(e) {
    if (!action) return;
    const { colStep, rowStep } = steps();
    const { it, kind } = action;
    const dx = Math.round((e.clientX - action.x0) / colStep);
    const dy = Math.round((e.clientY - action.y0) / rowStep);
    let next;
    if (kind === 'move') {
      next = { ...it, x: clamp(it.x + dx, 0, COLS - it.w), y: Math.max(0, it.y + dy) };
    } else {
      const w = clamp(it.w + dx, minOf(it.id).w, COLS - it.x);
      next = { ...it, w, h: Math.max(minOf(it.id, w).h, it.h + dy) };
      if (next.h !== it.h && it.id.startsWith('g')) next.fix = true;
    }
    const now = byId[it.id];
    if (preview && now.x === next.x && now.y === next.y && now.w === next.w && now.h === next.h) return;
    preview = settle(items, next);
  }

  async function end() {
    if (!action) return;
    const { it, kind } = action;
    const result = preview;
    action = null;
    preview = null;
    if (!result) return;
    onChange(toPositions(result));
    // A group only made wider or narrower (height not set by hand) takes the height its tiles now need.
    const now = result.find((i) => i.id === it.id);
    if (kind === 'size' && it.id.startsWith('g') && now.w !== it.w && now.h === it.h && !it.fix) {
      await tick();
      await new Promise(requestAnimationFrame);
      fit(it.id);
    }
  }

  function cancel() {
    action = null;
    preview = null;
  }

  // Keys: arrows move the block, with Shift they change its size.
  function key(e, it) {
    const d = { ArrowRight: [1, 0], ArrowLeft: [-1, 0], ArrowDown: [0, 1], ArrowUp: [0, -1] }[e.key];
    if (!d) return;
    e.preventDefault();
    const w = e.shiftKey ? clamp(it.w + d[0], minOf(it.id).w, COLS - it.x) : it.w;
    let next = e.shiftKey
      ? { ...it, w, h: Math.max(minOf(it.id, w).h, it.h + d[1]) }
      : { ...it, x: clamp(it.x + d[0], 0, COLS - it.w), y: Math.max(0, it.y + d[1]) };
    // Moving up or down onto another block jumps past it, instead of pushing
    // that block further down with every key press.
    if (!e.shiftKey && d[1]) {
      const others = items.filter((o) => o.id !== it.id);
      let hit;
      while ((hit = others.find((o) => overlaps(o, next)))) {
        const y = d[1] > 0 ? hit.y + hit.h : hit.y - next.h;
        if (y < 0) break;
        next = { ...next, y };
      }
    }
    if (e.shiftKey && d[1] && next.h !== it.h && it.id.startsWith('g')) next.fix = true;
    if (next.x !== it.x || next.y !== it.y || next.w !== it.w || next.h !== it.h) onChange(toPositions(settle(items, next)));
  }

  const area = (it) => `${it.y + 1} / ${it.x + 1} / span ${it.h} / span ${it.w}`;
</script>

<div class="pgrid" class:arranging bind:this={gridEl} style:--rows={rows}>
  {#if arranging}
    <div class="cells" aria-hidden="true">
      {#each { length: rows * COLS }, i (i)}<span></span>{/each}
    </div>
  {/if}
  {#each blocks as b (b.id)}
    {@const it = byId[b.id]}
    {#if it}
      <div class="pcell pb-{b.kind || b.id}" class:fixed={isFixed(b)} class:moving={action?.it.id === b.id} style:grid-area={area(it)} bind:this={cells[b.id]}>
        {@render block(b, it)}
        {#if arranging}
          <!-- div, not button: themes style every button (gradients, frames) and would cover the block. -->
          <div class="handle" class:low={it.h <= 1} role="button" tabindex="0" aria-label={t('{name} verschieben (Pfeiltasten, mit Umschalt die Größe)', { name: b.label })}
            onpointerdown={(e) => start(e, it, 'move')} onpointermove={move} onpointerup={end} onpointercancel={cancel} onkeydown={(e) => key(e, it)}>
            <span class="tag">{b.label}{#if tooSmall[b.id]}<span class="more" title={t('Die Höhe ist von Hand gesetzt und die Kacheln brauchen mehr Platz – sie scrollen in der Gruppe. Doppelklick auf die Ecke: wieder mit den Kacheln wachsen.')}> ↕</span>{/if}</span>
            {#if tools}
              <!-- A click on a button must not start dragging the block. -->
              <span class="tools" role="presentation" onpointerdown={(e) => e.stopPropagation()}>{@render tools(b)}</span>
            {/if}
          </div>
          <div class="resize" aria-hidden="true" title={t('Größe ändern – Doppelklick passt die Höhe an den Inhalt an')}
            onpointerdown={(e) => start(e, it, 'size')} onpointermove={move} onpointerup={end} onpointercancel={cancel} ondblclick={() => fit(b.id)}></div>
        {/if}
      </div>
    {/if}
  {/each}
</div>

<style>
  .pgrid {
    position: relative; display: grid; gap: 12px 12px;
    grid-template-columns: repeat(12, minmax(0, 1fr)); grid-template-rows: repeat(var(--rows), minmax(29px, auto));
  }
  .pgrid.arranging { grid-template-rows: repeat(var(--rows), 29px); }
  .cells {
    position: absolute; inset: 0; display: grid; gap: 12px; pointer-events: none;
    grid-template-columns: repeat(12, minmax(0, 1fr)); grid-template-rows: repeat(var(--rows), 29px);
  }
  .cells span { border: 1px dashed var(--border-strong); border-radius: var(--r-xs); opacity: .4; }
  /* Each block is a size container, so its content can follow its size. */
  .pcell { position: relative; min-width: 0; display: flex; flex-direction: column; justify-content: center; container-type: inline-size; }
  .pcell.pb-group { justify-content: flex-start; }
  .pcell > :global(*) { min-width: 0; }
  /* Fixed blocks keep the height they were given and scroll inside. */
  /* contain: size – their content must not make the rows taller. */
  .pcell.fixed { min-height: 0; justify-content: flex-start; contain: size; }
  .pcell.fixed > :global(*) { flex: 1; min-height: 0; }
  /* The plain search field keeps its own height, in the middle of its block (only the tall panel fills it). */
  .pcell.fixed > :global(.search:not(.tall)), .pcell.fixed > :global(.search-icon) { flex: none; margin: auto 0; }
  .pcell.fixed > :global(.search-icon) { height: min(100%, 44px); }
  /* A group smaller than its tiles scrolls inside; the scrollbar shows on hover. */
  .pcell.fixed.pb-group > :global(*) { padding: 4px; margin: -4px; overflow-y: auto; overflow-x: hidden; overscroll-behavior: contain; scrollbar-width: thin; scrollbar-color: transparent transparent; align-content: start; }
  .pcell.fixed.pb-group > :global(*:hover) { scrollbar-color: var(--border-strong) transparent; }
  .arranging .pcell { min-height: 0; overflow: hidden; border-radius: var(--radius); }
  .arranging .pcell > :global(*) { pointer-events: none; }
  /* While arranging every group has a fixed height, but it is only a preview:
     nothing inside scrolls, so the mouse wheel always scrolls the page. */
  .arranging .pcell.pb-group > :global(*), .arranging .pcell.pb-group :global(.tile), .arranging .pcell.pb-search :global(.panel) {
    overflow: hidden !important; overscroll-behavior: auto !important;
  }
  .arranging .pcell.moving { opacity: .5; }
  .handle {
    position: absolute; inset: 0; z-index: 3; padding: 0; border: 2px dashed color-mix(in srgb, var(--accent) 70%, transparent);
    border-radius: var(--radius); background: color-mix(in srgb, var(--accent) 7%, transparent); cursor: grab; touch-action: none;
    display: block; text-align: left; pointer-events: auto !important; outline-offset: -2px; box-sizing: border-box;
  }
  .handle:hover, .handle:focus-visible { background: color-mix(in srgb, var(--accent) 14%, transparent); border-color: var(--accent); }
  .handle:active { cursor: grabbing; }
  .tag {
    position: absolute; left: 6px; top: 6px; max-width: calc(100% - 12px); overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
    font-size: 11.5px; font-weight: 600; padding: 2px 8px; border-radius: 999px; background: var(--accent); color: var(--on-accent, #fff);
  }
  .tag .more { font-weight: 700; }
  .tools { position: absolute; right: 6px; top: 6px; display: flex; gap: 4px; cursor: default; }
  .tools:empty { display: none; }
  /* A block one row high: name and buttons in the middle and a bit smaller, so they stay inside. */
  .handle.low .tag, .handle.low .tools { top: 50%; transform: translateY(-50%); }
  .handle.low .tools { right: 4px; gap: 2px; }
  .handle.low .tools :global(.btool) { width: 20px; height: 20px; font-size: 11px; }
  /* A narrow block: the name gives way to the buttons. */
  .handle:has(.tools:not(:empty)) .tag { max-width: calc(100% - 76px); }
  .resize {
    position: absolute; right: 0; bottom: 0; z-index: 4; width: 24px; height: 24px; padding: 0;
    border: 0; background: none; cursor: nwse-resize; touch-action: none; pointer-events: auto !important;
  }
  .resize::after {
    content: ''; position: absolute; right: 6px; bottom: 6px; width: 9px; height: 9px;
    border-right: 2px solid var(--accent); border-bottom: 2px solid var(--accent); border-radius: 0 0 3px 0;
  }
</style>
