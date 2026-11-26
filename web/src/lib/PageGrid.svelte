<script module>
  import { COLS, overlaps, freeSpot } from './FreeGrid.svelte';
  import { pack, place, gapFor, closeGaps } from './pageLayout.js';

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
    // Stored places that overlap (an older layout, a block grown to its minimum) are pushed apart.
    const clash = grown || items.some((a, i) => items.some((b, j) => j > i && overlaps(a, b)));
    const placed = clash ? pack(items) : items;
    for (const b of blocks) {
      if (positions?.[b.id]) continue;
      const s = b.size || { w: 3, h: 2 };
      placed.push({ id: b.id, ...freeSpot(placed, Math.min(s.w, COLS), s.h) });
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
  let action = $state(null); // { kind: 'move' | 'size', it, x0, y0 } – y0 in page coordinates (scrolling while dragging counts)
  // Puts a block in place: what it lands on makes room (above it if there is
  // room, otherwise below); it stays exactly there, and what was below its old place follows up.
  const arrange = (list, moving) => place(list, moving);

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
  // nothing is ever cut off and nothing on the page scrolls by itself – a
  // scrolling group would catch the mouse wheel. A height set by hand is the
  // least it takes. Only while arranging all groups keep their rows.
  const isFixed = (b) => b.fixed || (b.kind === 'group' && arranging);

  // Rows a block's content needs at its current width.
  // While arranging a group fills its whole block (flex: 1), so its scrollHeight
  // would be the block's height, never less – a group could only grow, never
  // shrink back to its tiles. For the measurement it takes its own height.
  function needRows(id) {
    const el = cells[id]?.firstElementChild;
    if (!el) return 0;
    const { flex, height } = el.style;
    el.style.flex = 'none';
    el.style.height = 'auto';
    const h = el.scrollHeight;
    el.style.flex = flex;
    el.style.height = height;
    return Math.ceil((h + GAP) / (ROW + GAP));
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
    if (it && h && (h !== it.h || it.fix)) onChange(toPositions(arrange(items, { ...it, h, fix: false })));
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

  // A place for a new block that moves nothing: the first gap on the page it
  // fits in, otherwise below everything. Returns the positions of all blocks.
  export function insert(id, w, h) {
    return toPositions([...items, { id, ...gapFor(items, w, h) }]);
  }

  // A block that comes back (shown again): to its old place p. What took that
  // place meanwhile moves back below it, so the page looks as it did before.
  export function putBack(id, p) {
    const b = blocks.find((x) => x.id === id);
    const w = Math.min(COLS, Math.max(minSize(b, p.w, colPx).w, p.w));
    const h = Math.max(minSize(b, w, colPx).h, p.h);
    return toPositions(place(items.filter((i) => i.id !== id), { ...p, id, x: Math.min(p.x, COLS - w), w, h }));
  }

  // Scrolls a block into view (a new one may land below what is on the screen).
  export function reveal(id) {
    cells[id]?.scrollIntoView({ block: 'nearest', behavior: 'smooth' });
  }

  // The positions after a block left the page (removed or hidden): the rest moves up.
  export function without(id) {
    return toPositions(closeGaps(items, items.filter((i) => i.id !== id)));
  }

  function start(e, it, kind) {
    if (e.button !== 0 || !arranging) return;
    e.preventDefault();
    e.stopPropagation();
    e.currentTarget.setPointerCapture(e.pointerId);
    action = { kind, it, x0: e.clientX, y0: e.clientY + scrollY };
    last = e;
  }

  // Near the top or bottom edge of the window the page scrolls by itself while
  // dragging, faster the closer the pointer is – a block can travel the whole page in one go.
  let last = null;
  let scrolling = 0;
  const EDGE = 80;
  function autoScroll() {
    scrolling = 0;
    if (!action || !last) return;
    const y = last.clientY;
    const v = y < EDGE ? y - EDGE : y > innerHeight - EDGE ? y - (innerHeight - EDGE) : 0;
    if (!v) return;
    const before = scrollY;
    scrollBy(0, Math.max(-28, Math.min(28, Math.round(v / 2.5))));
    if (scrollY !== before) update(last);
    scrolling = requestAnimationFrame(autoScroll);
  }

  function move(e) {
    if (!action) return;
    last = e;
    update(e);
    if (!scrolling) scrolling = requestAnimationFrame(autoScroll);
  }

  function update(e) {
    const { colStep, rowStep } = steps();
    const { it, kind } = action;
    const dx = Math.round((e.clientX - action.x0) / colStep);
    const dy = Math.round((e.clientY + scrollY - action.y0) / rowStep);
    let next;
    if (kind === 'move') {
      next = { ...it, x: clamp(it.x + dx, 0, COLS - it.w), y: Math.max(0, it.y + dy) };
    } else {
      const w = clamp(it.w + dx, minOf(it.id).w, COLS - it.x);
      next = { ...it, w, h: Math.max(minOf(it.id, w).h, it.h + dy) };
      if (next.h !== it.h && it.id.startsWith('g')) next.fix = true;
    }
    const now = byId[it.id];
    if (preview && action.at && action.at.x === next.x && action.at.y === next.y && action.at.w === next.w && action.at.h === next.h) return;
    action.at = { x: next.x, y: next.y, w: next.w, h: next.h };
    preview = arrange(items, next);
  }

  async function end() {
    if (!action) return;
    const { it, kind } = action;
    const result = preview;
    action = null;
    preview = null;
    last = null;
    cancelAnimationFrame(scrolling);
    scrolling = 0;
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
    last = null;
    cancelAnimationFrame(scrolling);
    scrolling = 0;
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
    if (next.x !== it.x || next.y !== it.y || next.w !== it.w || next.h !== it.h) onChange(toPositions(arrange(items, next)));
  }

  const area = (it) => `${it.y + 1} / ${it.x + 1} / span ${it.h} / span ${it.w}`;
</script>

<svelte:window onscroll={() => action && last && update(last)} />

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
            <span class="tag">{b.label}{#if tooSmall[b.id]}<span class="more" title={t('Die Kacheln brauchen mehr Platz als diese Höhe – auf der Seite wird die Gruppe so hoch, wie sie es braucht. Doppelklick auf die Ecke: Höhe an die Kacheln anpassen.')}> ↕</span>{/if}</span>
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
  .pcell.fixed { min-height: 0; justify-content: flex-start; contain: size; container-type: size; }
  .pcell.fixed > :global(*) { flex: 1; min-height: 0; }
  /* The plain search field keeps its own height, in the middle of its block (only the tall panel fills it). */
  .pcell.fixed > :global(.search:not(.tall)), .pcell.fixed > :global(.search-icon) { flex: none; margin: auto 0; }
  /* The search follows its block: the field as wide as the block and taller with more rows. */
  .pcell.pb-search > :global(.search:not(.tall)) { max-width: none; --field-h: clamp(40px, 62cqh, 64px); }
  .pcell.pb-search > :global(.search:not(.tall) > input) { height: var(--field-h); font-size: clamp(14px, calc(var(--field-h) * .36), 21px); padding-left: calc(var(--field-h) * .9); }
  .pcell.pb-search > :global(.search:not(.tall) .glass) { left: calc(var(--field-h) * .32); }
  .pcell.fixed > :global(.search-icon) { height: clamp(36px, 80cqh, 64px); }
  /* While arranging a group shows what fits into its rows (a preview, it never scrolls). */
  .pcell.fixed.pb-group > :global(*) { overflow: hidden; align-content: start; }
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
