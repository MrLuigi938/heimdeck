// Layout rules of the free page: blocks on a grid of 12 columns and rows.
// A block stays exactly where it is put – gaps the user leaves stay. Only space
// a change has just freed (a block moved away, hidden, removed or made
// smaller) is filled: the blocks right below it follow up.
// Plain JavaScript without imports, so the rules can be tested on their own
// (same as COLS and overlaps in FreeGrid.svelte).
const COLS = 12;
const overlaps = (a, b) => a.x < b.x + b.w && b.x < a.x + a.w && a.y < b.y + b.h && b.y < a.y + a.h;
const cells = (list) => {
  const taken = new Set();
  for (const b of list) for (let y = b.y; y < b.y + b.h; y++) for (let x = b.x; x < b.x + b.w; x++) taken.add(x + ',' + y);
  return taken;
};

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

// Moving up into space a change has just freed: `before` is the layout before
// the change, `after` the one after it. A block moves up one row at a time
// while the row above it is free now and it was resting on something there
// before (a cell of that row, above the block, was taken before) – so what was
// below a block that left follows up, also when it is wider than that block
// and the rest of the row was empty (two blocks side by side above it), while
// a gap that was already there above a block stays. The block that was just
// placed (keep) stays exactly where it was put.
export function closeGaps(before, after, keep = null) {
  const taken = cells(before);
  const list = after.map((i) => ({ ...i })).sort((a, b) => a.y - b.y || a.x - b.x);
  let moved = true;
  while (moved) {
    moved = false;
    for (const it of list) {
      if (it.id === keep) continue;
      while (it.y > 0) {
        const y = it.y - 1;
        let rested = false;
        for (let x = it.x; x < it.x + it.w && !rested; x++) rested = taken.has(x + ',' + y);
        if (!rested || list.some((o) => o !== it && overlaps(o, { ...it, y }))) break;
        it.y = y;
        moved = true;
      }
    }
  }
  return list;
}

// Puts `moving` exactly where it was dropped. A block it lands on that starts
// higher than it goes above it if there is room there (so a block dragged
// down past its neighbour swaps places with it instead of pushing it along),
// every other block it covers goes below it. Then what was below its old place
// follows up.
export function place(items, moving) {
  const placed = [{ ...moving }];
  const rest = items.filter((i) => i.id !== moving.id).map((i) => ({ ...i })).sort((a, b) => a.y - b.y || a.x - b.x);
  for (const it of rest) {
    if (overlaps(moving, it) && it.y < moving.y) {
      const above = { ...it, y: moving.y - it.h };
      if (above.y >= 0 && !placed.some((p) => overlaps(p, above))) {
        placed.push(above);
        continue;
      }
    }
    let hit;
    while ((hit = placed.filter((p) => overlaps(p, it))).length) it.y = Math.max(...hit.map((p) => p.y + p.h));
    placed.push(it);
  }
  return closeGaps(items, placed, moving.id);
}

// A spot for a new block that moves nothing: the first gap it fits in, top to
// bottom, otherwise below everything.
export function gapFor(items, w, h) {
  w = Math.min(w, COLS);
  const bottom = Math.max(0, ...items.map((i) => i.y + i.h));
  for (let y = 0; y + h <= bottom; y++) {
    for (let x = 0; x + w <= COLS; x++) {
      const c = { x, y, w, h };
      if (!items.some((i) => overlaps(i, c))) return c;
    }
  }
  return { x: 0, y: bottom, w, h };
}
