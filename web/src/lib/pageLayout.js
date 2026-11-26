// Layout rules of the free page: blocks on a grid of 12 columns and rows.
// Blocks always move up as far as they can (like things falling upwards):
// whatever frees space – a block moved away, hidden, removed or made smaller –
// lets the blocks below follow up, also next to blocks that sit side by side.
// Plain JavaScript without imports, so the rules can be tested on their own
// (same as COLS and overlaps in FreeGrid.svelte).
const COLS = 12;
const overlaps = (a, b) => a.x < b.x + b.w && b.x < a.x + a.w && a.y < b.y + b.h && b.y < a.y + a.h;

// Moves every block up until it touches the block above it (or the top).
// Blocks never jump over each other, so the reading order stays.
export function compact(items) {
  const placed = [];
  for (const it of [...items].sort((a, b) => a.y - b.y || a.x - b.x).map((i) => ({ ...i }))) {
    while (it.y > 0 && !placed.some((p) => overlaps(p, { ...it, y: it.y - 1 }))) it.y--;
    placed.push(it);
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

// Puts `moving` where it was dropped. A block it lands on that starts higher
// than it goes above it if there is room there (so a block dragged down past
// its neighbour swaps places with it instead of pushing it along), every other
// block it covers goes below it. Then everything moves up.
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
  return compact(placed);
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
