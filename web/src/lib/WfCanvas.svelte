<script>
  // The canvas of the large editor: the workflow from left to right as blocks
  // with curved connections – triggers, how they are joined, conditions,
  // steps, "if" splitting into yes and no. Drag the background to move,
  // the wheel to zoom, a block to place it; a tap opens it on the right.
  // Blocks come from the side bar (a tap adds at the end, dragging one onto a
  // ＋ puts it in that place). The places are kept with the workflow.
  import { t } from './i18n.svelte.js';
  import { blankTrigger, blankCond, blankStep, triggerText, triggerIcon, condText, stepText, hostOf } from './workflow.js';
  import WfTrigger from './WfTrigger.svelte';
  import WfCond from './WfCond.svelte';
  import WfStep from './WfStep.svelte';

  // fill: the canvas takes the height it is given (the whole page); panel: a
  // snippet shown on the right while no block is chosen (the settings).
  let { wf = $bindable(), tiles = [], scenes = [], ai = null, live = null, fill = false, panel = null } = $props();

  const W = 210, H = 74, GX = 80, ROW = 104;
  const JW = 64; // the join node
  if (!wf.layout) wf.layout = {};

  let box = $state();
  let boxW = $state(1200);
  let boxH = $state(700);
  let narrow = $derived(boxW < 900); // the side bar shows only its symbols
  // On a phone the panel is not docked: it opens as a sheet from below on request
  // (and when a block is chosen), so it never covers the blocks for good.
  let small = $derived(boxW < 760);
  let panelOpen = $state(false);
  let docked = $derived(!!panel && !small);
  $effect(() => {
    if (!box) return;
    const ro = new ResizeObserver(() => {
      boxW = box.clientWidth;
      boxH = box.clientHeight;
    });
    ro.observe(box);
    return () => ro.disconnect();
  });

  // ---- the drawing, worked out from the workflow ----
  const tone = (s) => (s.kind === 'wait' ? 'neutral' : s.kind === 'if' ? 'cond' : 'act');
  const kindName = (s) => ({ notify: t('Nachricht'), button: t('Knopf'), http: t('Adresse aufrufen'), scene: t('Szene'), wait: t('Warten'), if: t('Wenn … sonst') })[s.kind] || t('Schritt');
  const stepIcon = (s) => ({ notify: '✉', button: '▶', http: '🌐', scene: '🎬', wait: '⏳', if: '❓' })[s.kind] || '•';
  const stepLabel = (s) => {
    if (s.kind === 'http') return (s.method || 'POST') + ' ' + (hostOf(s.url) || '…');
    if (s.kind === 'if') return condText(s.if || {}, tiles);
    if (s.kind === 'notify') return s.text || (s.report ? t('Lagebericht') : t('Nachricht'));
    return stepText(s, tiles, scenes).replace(/^\S+\s/, '');
  };

  let graph = $derived.by(() => {
    const nodes = [];
    const edges = [];
    const on = live && !live.problem;
    const pass = on ? !!live.conds : null; // the conditions hold now
    let col = 0;
    // Like text, the flow wraps into rows that fit the canvas.
    const hasIf = wf.steps.some((s) => s.kind === 'if');
    const rowH = Math.max(hasIf ? 300 : 200, wf.triggers.length * ROW + 70);
    // How many blocks per row: the number at which the whole drawing shows largest.
    const cols = (wf.triggers.length > 1 ? 2 : 1) + wf.conds.length + 1 +
      wf.steps.reduce((n, s) => n + 1 + (s.kind === 'if' ? Math.max(1, s.then.length, s.else.length) : 0), 0);
    const availW = boxW - (narrow ? 70 : 200) - (docked ? 360 : 0); // the side bar on the left, the panel on the right
    const availH = boxH - 110;
    let perRow = 2, best = 0;
    for (let p = 2; p <= Math.max(2, cols); p++) {
      const z = Math.min(1.2, availW / (p * (W + GX) - GX), availH / (Math.ceil(cols / p) * rowH - (rowH - H - 60)));
      if (z > best + 0.01) {
        best = z;
        perRow = p;
      }
    }
    const P = (c, ly) => ({ x: (c % perRow) * (W + GX), y: Math.floor(c / perRow) * rowH + ly });
    // triggers
    const n = wf.triggers.length;
    wf.triggers.forEach((tr, i) => {
      const held = on ? live.triggers?.[i] : null;
      nodes.push({ key: 't' + i, sel: { kind: 'trigger', i }, ...P(0, (i - (n - 1) / 2) * ROW - H / 2), tone: 'trig', kind: t('Auslöser'), icon: triggerIcon(tr.kind), label: triggerText(tr, tiles), mark: held === true ? 'ok' : held === false ? 'no' : '' });
    });
    let prev = n === 1 ? ['t0'] : wf.triggers.map((_, i) => 't' + i);
    col = 1;
    if (n > 1) {
      nodes.push({ key: 'j', join: true, x: P(1, 0).x + (W - JW) / 2, y: P(1, -JW / 2).y, w: JW, h: JW });
      for (const p of prev) edges.push({ from: p, to: 'j' });
      prev = ['j'];
      col = 2;
    }
    // conditions, one after the other
    wf.conds.forEach((c, i) => {
      const held = on ? live.condList?.[i] : null;
      nodes.push({ key: 'c' + i, sel: { kind: 'cond', i }, ...P(col, -H / 2), tone: 'cond', kind: t('Nur wenn'), icon: '✔', label: condText(c, tiles), mark: held === true ? 'ok' : held === false ? 'no' : '' });
      for (const p of prev) edges.push({ from: p, to: 'c' + i });
      prev = ['c' + i];
      col++;
    });
    // steps
    let branchNo = 0;
    let active = pass !== false; // the run would get here
    wf.steps.forEach((s, i) => {
      const key = 's' + i;
      nodes.push({ key, sel: { kind: 'step', i }, ...P(col, -H / 2), tone: tone(s), kind: kindName(s), icon: stepIcon(s), label: stepLabel(s), dim: on && !active });
      for (const p of prev) edges.push({ from: p.key || p, to: key, add: { i }, on: on && active && !p.off, dim: on && (!active || p.off) });
      prev = [key];
      col++;
      if (s.kind === 'if') {
        const way = on && active ? live.branches?.[branchNo] : undefined;
        branchNo++;
        const ends = [];
        let len = 1;
        for (const [branch, dy, label] of [['then', -ROW * 0.62, t('ja')], ['else', ROW * 0.62, t('nein')]]) {
          const taken = way === undefined ? null : way === (branch === 'then');
          let from = key;
          s[branch].forEach((x2, j) => {
            const k = key + branch[0] + j;
            nodes.push({ key: k, sel: { kind: 'step', i, branch, j }, ...P(col + j, dy - H / 2), tone: tone(x2), kind: kindName(x2), icon: stepIcon(x2), label: stepLabel(x2), dim: taken === false || on && !active });
            edges.push({ from, to: k, label: j === 0 ? label : '', add: { i, branch, j }, on: taken === true, dim: taken === false });
            from = k;
          });
          len = Math.max(len, s[branch].length);
          ends.push({ key: from, branch, empty: !s[branch].length, label, taken });
        }
        // after the branches the run goes on with the next step
        col += len;
        prev = ends.map((e) => ({ key: e.key, off: e.taken === false, label: e.empty ? e.label : '', branch: e.branch, empty: e.empty, i }));
      }
    });
    // the end: ＋ for a new step
    nodes.push({ key: 'end', end: true, ...P(col, -22), w: 44, h: 44 });
    for (const p of prev) {
      const k = p.key || p;
      const add = p.branch ? { i: p.i, branch: p.branch, j: wf.steps[p.i][p.branch].length } : { i: wf.steps.length };
      edges.push({ from: k, to: 'end', label: p.label || '', add, on: on && active && !p.off, dim: on && (!active || p.off), branchEnd: !!p.branch });
    }
    // places the person chose
    for (const nd of nodes) {
      const at = wf.layout?.[nd.key];
      if (at) {
        nd.x = at.x;
        nd.y = at.y;
      }
      nd.w ??= W;
      nd.h ??= H;
    }
    const byKey = Object.fromEntries(nodes.map((nd) => [nd.key, nd]));
    return { nodes, edges, byKey };
  });

  function path(e) {
    const a = graph.byKey[e.from], b = graph.byKey[e.to];
    if (!a || !b) return null;
    const x1 = a.x + a.w, y1 = a.y + a.h / 2, x2 = b.x, y2 = b.y + b.h / 2;
    if (x2 < x1 - 10) {
      // into the next row: out to the right, back along the gap between the rows, in from the left
      const ym = (y1 + y2) / 2;
      return { d: `M${x1},${y1} C${x1 + 70},${y1} ${x1 + 70},${ym} ${x1},${ym} L${x2},${ym} C${x2 - 70},${ym} ${x2 - 70},${y2} ${x2},${y2}`, mx: (x1 + x2) / 2, my: ym };
    }
    const dx = Math.max(40, Math.abs(x2 - x1) / 2);
    return { d: `M${x1},${y1} C${x1 + dx},${y1} ${x2 - dx},${y2} ${x2},${y2}`, mx: (x1 + x2) / 2, my: (y1 + y2) / 2 };
  }

  // ---- moving and zooming ----
  let view = $state({ x: 60, y: 260, z: 1 });
  let sel = $state(null);
  let fitted = false;

  function fit() {
    if (!box) return;
    const ns = graph.nodes;
    const minX = Math.min(...ns.map((n) => n.x)), maxX = Math.max(...ns.map((n) => n.x + n.w));
    const minY = Math.min(...ns.map((n) => n.y)), maxY = Math.max(...ns.map((n) => n.y + n.h));
    const r = box.getBoundingClientRect();
    const free = !small && (sel || panel) ? r.width - 360 : r.width; // the side panel covers the right
    const left = narrow ? 64 : 180; // the side bar
    const top = 48, bottom = 60; // the legend above, the zoom buttons below
    const z = Math.min(1.2, (free - left - 30) / (maxX - minX + 1), (r.height - top - bottom) / (maxY - minY + 1));
    const zz = Math.max(0.4, z);
    view = { z: zz, x: left + (free - left - 20 - (maxX - minX) * zz) / 2 - minX * zz, y: top + (r.height - top - bottom - (maxY - minY) * zz) / 2 - minY * zz };
  }
  $effect(() => {
    if (box && !fitted && graph.nodes.length) {
      fitted = true;
      boxW = box.clientWidth; // the real size first: the rows depend on it
      boxH = box.clientHeight;
      requestAnimationFrame(fit);
    }
  });
  // A block more or less fits the drawing again – unless the person moved or zoomed it.
  let lastCount = 0;
  $effect(() => {
    const c = graph.nodes.length;
    if (fitted && !touched && lastCount && c !== lastCount) requestAnimationFrame(fit);
    lastCount = c;
  });
  // A new window size fits the drawing again – unless the person moved or zoomed it.
  let touched = false;
  let lastW = 0;
  $effect(() => {
    const w = boxW;
    if (fitted && !touched && Math.abs(w - lastW) > 8) requestAnimationFrame(fit);
    lastW = w;
  });
  function zoomAt(f, cx, cy) {
    const z = Math.min(2, Math.max(0.3, view.z * f));
    view = { z, x: cx - ((cx - view.x) * z) / view.z, y: cy - ((cy - view.y) * z) / view.z };
  }
  function onWheel(e) {
    if (e.target.closest?.('.inspector, .palette')) return;
    e.preventDefault();
    const r = box.getBoundingClientRect();
    touched = true;
    zoomAt(e.deltaY < 0 ? 1.1 : 1 / 1.1, e.clientX - r.left, e.clientY - r.top);
  }
  // The wheel zooms the canvas, not the page (so the listener must not be passive).
  $effect(() => {
    if (!box) return;
    box.addEventListener('wheel', onWheel, { passive: false });
    return () => box.removeEventListener('wheel', onWheel);
  });
  function zoomBtn(f) {
    const r = box.getBoundingClientRect();
    zoomAt(f, r.width / 2, r.height / 2);
  }

  let drag = $state(null); // { kind: 'pan' | 'node', … }
  function downBg(e) {
    if (e.button !== 0) return;
    drag = { kind: 'pan', sx: e.clientX, sy: e.clientY, vx: view.x, vy: view.y };
    e.currentTarget.setPointerCapture(e.pointerId);
  }
  function downNode(e, nd) {
    if (e.button !== 0) return;
    e.stopPropagation();
    drag = { kind: 'node', nd, sx: e.clientX, sy: e.clientY, x: nd.x, y: nd.y, moved: false };
    e.currentTarget.setPointerCapture(e.pointerId);
  }
  function move(e) {
    if (!drag) return;
    const dx = e.clientX - drag.sx, dy = e.clientY - drag.sy;
    if (drag.kind === 'pan') {
      view = { ...view, x: drag.vx + dx, y: drag.vy + dy };
      touched = true;
    }
    else if (drag.moved || Math.abs(dx) + Math.abs(dy) > 4) {
      drag.moved = true;
      wf.layout[drag.nd.key] = { x: Math.round(drag.x + dx / view.z), y: Math.round(drag.y + dy / view.z) };
      // A step held over a ＋ will move there when let go.
      if (drag.nd.sel?.kind === 'step') {
        // The held block itself is under the pointer – look through it.
        const hit = document.elementsFromPoint(e.clientX, e.clientY).map((el) => el.closest?.('.plus')).find(Boolean);
        moveTarget = hit ? JSON.parse(hit.dataset.at) : null;
      }
    }
  }
  let moveTarget = $state(null);
  function up() {
    if (drag?.kind === 'node' && !drag.moved) {
      const nd = drag.nd;
      if (nd.join) wf.all = !wf.all;
      else if (nd.end) addStep({ i: wf.steps.length }, 'notify');
      else sel = nd.sel;
    } else if (drag?.kind === 'node' && moveTarget) {
      moveStep(drag.nd.sel, moveTarget);
    }
    moveTarget = null;
    drag = null;
  }
  // moveStep takes a step out of its place and puts it in front of the ＋ it was dropped on.
  function moveStep(from, at) {
    const src = from.branch ? wf.steps[from.i][from.branch] : wf.steps;
    const dst = at.branch ? wf.steps[at.i]?.[at.branch] : wf.steps;
    const step = src[from.branch ? from.j : from.i];
    if (!dst || !step) return;
    if (at.branch && (step.kind === 'if' || !from.branch && from.i === at.i)) return; // no "if" in an "if", not into itself
    let to = at.branch ? at.j : at.i;
    const fromIdx = from.branch ? from.j : from.i;
    if (src === dst && fromIdx < to) to--;
    if (src === dst && fromIdx === to) {
      delete wf.layout[graphKey(from)];
      return;
    }
    src.splice(fromIdx, 1);
    dst.splice(to, 0, step);
    forget('s');
    sel = null;
  }
  const graphKey = (s) => (s.branch ? 's' + s.i + s.branch[0] + s.j : 's' + s.i);

  // ---- changing the workflow ----
  // Steps that move lose their chosen place (it belonged to another step).
  const forget = (prefix) => {
    for (const k of Object.keys(wf.layout)) if (k.startsWith(prefix) || k === 'end') delete wf.layout[k];
  };
  function addStep(at, kind) {
    const list = at.branch ? wf.steps[at.i][at.branch] : wf.steps;
    const step = blankStep(at.branch && kind === 'if' ? 'notify' : kind);
    list.splice(at.branch ? at.j : at.i, 0, step);
    forget('s');
    sel = at.branch ? { kind: 'step', i: at.i, branch: at.branch, j: at.j } : { kind: 'step', i: at.i };
  }
  function add(kind) {
    if (kind === 'trigger') {
      if (wf.triggers.length >= 5) return;
      wf.triggers.push(blankTrigger());
      forget('t');
      delete wf.layout.j;
      sel = { kind: 'trigger', i: wf.triggers.length - 1 };
    } else if (kind === 'cond') {
      if (wf.conds.length >= 5) return;
      wf.conds.push(blankCond());
      forget('c');
      forget('s');
      sel = { kind: 'cond', i: wf.conds.length - 1 };
    } else addStep({ i: wf.steps.length }, kind);
  }
  function remove() {
    if (!sel) return;
    if (sel.kind === 'trigger') {
      if (wf.triggers.length < 2) return;
      wf.triggers.splice(sel.i, 1);
      forget('t');
    } else if (sel.kind === 'cond') {
      wf.conds.splice(sel.i, 1);
      forget('c');
      forget('s');
    } else if (sel.branch) wf.steps[sel.i][sel.branch].splice(sel.j, 1);
    else wf.steps.splice(sel.i, 1);
    if (sel.kind === 'step') forget('s');
    sel = null;
  }
  function tidy() {
    wf.layout = {};
    touched = false;
    requestAnimationFrame(fit);
  }
  function onKey(e) {
    if (e.target.matches?.('input, textarea, select')) return;
    if ((e.key === 'Delete' || e.key === 'Backspace') && sel) {
      e.preventDefault();
      remove();
    } else if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'z') {
      e.preventDefault();
      if (e.shiftKey) redo();
      else undo();
    } else if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'y') {
      e.preventDefault();
      redo();
    }
  }

  // ---- undo and redo: every change, kept as a snapshot ----
  const KEYS = ['name', 'triggers', 'all', 'conds', 'steps', 'only', 'cooldown', 'noAsk', 'layout'];
  const snap = () => JSON.stringify(Object.fromEntries(KEYS.map((k) => [k, $state.snapshot(wf[k])])));
  let past = $state([]);
  let future = $state([]);
  let current = snap();
  let restoring = false;
  $effect(() => {
    const now = snap();
    if (restoring || now === current) {
      restoring = false;
      current = now;
      return;
    }
    const timer = setTimeout(() => {
      past = [...past.slice(-49), current];
      future = [];
      current = now;
    }, 350);
    return () => clearTimeout(timer);
  });
  function apply(state) {
    restoring = true;
    const o = JSON.parse(state);
    for (const k of KEYS) wf[k] = o[k];
    current = state;
    sel = null;
  }
  function undo() {
    if (!past.length) return;
    future = [current, ...future];
    const prev = past[past.length - 1];
    past = past.slice(0, -1);
    apply(prev);
  }
  function redo() {
    if (!future.length) return;
    past = [...past, current];
    const next = future[0];
    future = future.slice(1);
    apply(next);
  }

  // ---- the mini map: the whole drawing and the part on screen ----
  const MW = 150, MH = 92;
  let mini = $derived.by(() => {
    const ns = graph.nodes;
    if (!ns.length) return null;
    const minX = Math.min(...ns.map((n) => n.x)) - 40, maxX = Math.max(...ns.map((n) => n.x + n.w)) + 40;
    const minY = Math.min(...ns.map((n) => n.y)) - 40, maxY = Math.max(...ns.map((n) => n.y + n.h)) + 40;
    const k = Math.min(MW / (maxX - minX), MH / (maxY - minY));
    const vx = -view.x / view.z, vy = -view.y / view.z, vw = boxW / view.z, vh = boxH / view.z;
    return { k, minX, minY, ns, view: { x: (vx - minX) * k, y: (vy - minY) * k, w: vw * k, h: vh * k } };
  });
  function miniGo(e) {
    if (!mini) return;
    const r = e.currentTarget.getBoundingClientRect();
    const wx = mini.minX + (e.clientX - r.left) / mini.k, wy = mini.minY + (e.clientY - r.top) / mini.k;
    view = { ...view, x: boxW / 2 - wx * view.z, y: boxH / 2 - wy * view.z };
    touched = true;
  }

  // Blocks dragged from the side bar onto a ＋ or the canvas.
  const palette = [
    { kind: 'trigger', icon: '⚡', label: t('Auslöser'), tone: 'trig' },
    { kind: 'cond', icon: '✔', label: t('Bedingung'), tone: 'cond' },
    { kind: 'notify', icon: '✉', label: t('Nachricht'), tone: 'act' },
    { kind: 'button', icon: '▶', label: t('Knopf'), tone: 'act' },
    { kind: 'http', icon: '🌐', label: t('Adresse'), tone: 'act' },
    { kind: 'scene', icon: '🎬', label: t('Szene'), tone: 'act' },
    { kind: 'wait', icon: '⏳', label: t('Warten'), tone: 'neutral' },
    { kind: 'if', icon: '❓', label: t('Wenn … sonst'), tone: 'cond' },
  ];
  let dragKind = $state('');
  function dropOn(e, at) {
    e.preventDefault();
    e.stopPropagation();
    const kind = e.dataTransfer.getData('text/x-wf') || dragKind;
    dragKind = '';
    if (!kind) return;
    if (kind === 'trigger' || kind === 'cond') add(kind);
    else addStep(at, kind);
  }
  function dropBg(e) {
    e.preventDefault();
    const kind = e.dataTransfer.getData('text/x-wf') || dragKind;
    dragKind = '';
    if (kind) add(kind);
  }

  const title = (s) => (s?.kind === 'trigger' ? t('Auslöser') : s?.kind === 'cond' ? t('Bedingung') : t('Schritt'));
  const same = (a, b) => a && b && a.kind === b.kind && a.i === b.i && a.branch === b.branch && a.j === b.j;
</script>

<svelte:window onkeydown={onKey} />

<div class="canvas" class:fill bind:this={box} onpointerdown={downBg} onpointermove={move} onpointerup={up} onpointercancel={up}
  ondragover={(e) => e.preventDefault()} ondrop={dropBg} class:panning={drag?.kind === 'pan'} role="application" aria-label={t('Workflow-Leinwand')}>
  <div class="world" style="transform: translate({view.x}px, {view.y}px) scale({view.z}); --grid: {18 * view.z}px">
    <svg class="edges" aria-hidden="true">
      {#each graph.edges as e, k (k)}
        {@const p = path(e)}
        {#if p}<path d={p.d} class:on={e.on} class:dim={e.dim} />{/if}
      {/each}
    </svg>
    {#each graph.edges as e, k (k)}
      {@const p = path(e)}
      {#if p && (e.label || e.add)}
        <div class="mid" style="left: {p.mx}px; top: {p.my}px">
          {#if e.label}<span class="elabel" class:on={e.on}>{e.label}</span>{/if}
          {#if e.add}
            <button class="plus" data-at={JSON.stringify(e.add)} class:target={dragKind && dragKind !== 'trigger' && dragKind !== 'cond' || moveTarget && JSON.stringify(moveTarget) === JSON.stringify(e.add)} class:moving={drag?.kind === 'node' && drag.moved && drag.nd.sel?.kind === 'step'} onpointerdown={(ev) => ev.stopPropagation()}
              onclick={() => addStep(e.add, 'notify')} ondragover={(ev) => ev.preventDefault()} ondrop={(ev) => dropOn(ev, e.add)}
              title={t('Schritt hier einfügen')} aria-label={t('Schritt hier einfügen')}>＋</button>
          {/if}
        </div>
      {/if}
    {/each}
    {#each graph.nodes as nd (nd.key)}
      {#if nd.join}
        <button class="join" style="left: {nd.x}px; top: {nd.y}px" onpointerdown={(e) => downNode(e, nd)} title={t('Zwischen „oder“ und „und“ wechseln')}>{wf.all ? t('und') : t('oder')}</button>
      {:else if nd.end}
        <button class="endnode" style="left: {nd.x}px; top: {nd.y}px" onpointerdown={(e) => downNode(e, nd)} ondragover={(ev) => ev.preventDefault()} ondrop={(ev) => dropOn(ev, { i: wf.steps.length })} title={t('Schritt anhängen')} aria-label={t('Schritt anhängen')}>＋</button>
      {:else}
        <button class="node {nd.tone}" class:sel={same(sel, nd.sel)} class:dim={nd.dim} style="left: {nd.x}px; top: {nd.y}px; width: {W}px; height: {H}px" onpointerdown={(e) => downNode(e, nd)}>
          <span class="kind">{nd.icon} {nd.kind}</span>
          <span class="label">{nd.label}</span>
          {#if nd.mark}<span class="mark {nd.mark}">{nd.mark === 'ok' ? '✓' : '✕'}</span>{/if}
        </button>
      {/if}
    {/each}
  </div>

  <div class="palette" class:narrow onpointerdown={(e) => e.stopPropagation()} role="toolbar" tabindex="-1" aria-label={t('Bausteine')}>
    {#if !narrow}<span class="ptitle">{t('Bausteine')}</span>{/if}
    {#each palette as p (p.kind)}
      <button class="pitem {p.tone}" draggable="true" ondragstart={(e) => { e.dataTransfer.setData('text/x-wf', p.kind); dragKind = p.kind; }} ondragend={() => (dragKind = '')} onclick={() => add(p.kind)} title={p.label + ' – ' + t('Antippen: ans Ende – oder auf ein ＋ ziehen')} aria-label={p.label}>
        <span>{p.icon}</span>{#if !narrow} {p.label}{/if}
      </button>
    {/each}
  </div>

  <div class="controls" class:center={narrow} class:middle={!narrow} style:left={!narrow ? (docked ? 'calc((100% - 180px - 370px) / 2 + 180px)' : 'calc((100% - 180px) / 2 + 180px)') : null} onpointerdown={(e) => e.stopPropagation()} role="group">
    <button class="small" onclick={() => zoomBtn(1 / 1.2)} aria-label={t('Verkleinern')}>−</button>
    <span class="zoom">{Math.round(view.z * 100)} %</span>
    <button class="small" onclick={() => zoomBtn(1.2)} aria-label={t('Vergrößern')}>＋</button>
    <button class="small" onclick={fit}>{t('Einpassen')}</button>
    <button class="small" onclick={tidy} title={t('Alle Bausteine wieder automatisch anordnen')}>{t('Aufräumen')}</button>
    <button class="small" onclick={undo} disabled={!past.length} aria-label={t('Rückgängig')} title={t('Rückgängig (Strg+Z)')}>↶</button>
    <button class="small" onclick={redo} disabled={!future.length} aria-label={t('Wiederholen')} title={t('Wiederholen (Strg+Y)')}>↷</button>
  </div>

  {#if mini && !narrow}
    <svg class="minimap" class:left={docked} width={MW} height={MH} onpointerdown={(e) => { e.stopPropagation(); miniGo(e); }} role="img" aria-label={t('Übersicht')}>
      {#each mini.ns as n (n.key)}
        <rect x={(n.x - mini.minX) * mini.k} y={(n.y - mini.minY) * mini.k} width={Math.max(2, n.w * mini.k)} height={Math.max(2, n.h * mini.k)} rx="1.5" class={n.tone || 'neutral'} />
      {/each}
      <rect class="vp" x={mini.view.x} y={mini.view.y} width={mini.view.w} height={mini.view.h} />
    </svg>
  {/if}

  {#if live && !live.problem}
    <div class="legend" style:left={narrow ? '60px' : null} onpointerdown={(e) => e.stopPropagation()} role="note">
      <span class="ln on"></span> {t('so würde es jetzt laufen')}
      {#if !live.conds}<span class="bad">· {t('die Bedingungen gelten gerade nicht')}</span>{/if}
    </div>
  {/if}

  {#if panel && small && !sel && !panelOpen}
    <button class="sheetbtn" onpointerdown={(e) => e.stopPropagation()} onclick={() => (panelOpen = true)}>
      ⚙ {t('Workflow')}{#if live?.problem}&nbsp;· ✎{:else if live}&nbsp;· ✓{/if}
    </button>
  {/if}
  <aside class="inspector" class:sheet={small} class:open={sel || (panel && (!small || panelOpen))} onpointerdown={(e) => e.stopPropagation()} onwheel={(e) => e.stopPropagation()}>
    {#if sel}
      <div class="ihead">
        <strong>{title(sel)}</strong>
        <span class="grow"></span>
        {#if !(sel.kind === 'trigger' && wf.triggers.length < 2)}<button class="small" onclick={remove}>{t('Entfernen')}</button>{/if}
        <button class="small ghost" onclick={() => (sel = null)} aria-label={t('Schließen')} title={t('Schließen')}>✕</button>
      </div>
      {#if sel.kind === 'trigger' && wf.triggers[sel.i]}
        <WfTrigger bind:tr={wf.triggers[sel.i]} {tiles} {ai} />
      {:else if sel.kind === 'cond' && wf.conds[sel.i]}
        <WfCond bind:cond={wf.conds[sel.i]} {tiles} />
      {:else if sel.kind === 'step' && sel.branch && wf.steps[sel.i]?.[sel.branch]?.[sel.j]}
        <WfStep bind:step={wf.steps[sel.i][sel.branch][sel.j]} {tiles} {scenes} {ai} nested />
      {:else if sel.kind === 'step' && !sel.branch && wf.steps[sel.i]}
        <WfStep bind:step={wf.steps[sel.i]} {tiles} {scenes} {ai} />
      {/if}
    {:else if panel}
      <div class="ihead"><strong>{t('Workflow')}</strong><span class="grow"></span>
        {#if small}<button class="small ghost" onclick={() => (panelOpen = false)} aria-label={t('Schließen')} title={t('Schließen')}>✕</button>
        {:else}<span class="muted">{t('Tippe auf einen Baustein, um ihn zu bearbeiten')}</span>{/if}
      </div>
      {@render panel()}
    {/if}
  </aside>
</div>

<style>
  .canvas {
    position: relative; height: min(620px, 70vh); min-height: 380px; overflow: hidden; touch-action: none; user-select: none;
    border: 1px solid var(--border); border-radius: var(--r-sm); cursor: grab;
    background-color: var(--surface-2);
    background-image: radial-gradient(color-mix(in srgb, var(--muted) 45%, transparent) 1px, transparent 1.4px);
    background-size: 18px 18px;
  }
  .canvas.fill { height: auto; min-height: 0; }
  .canvas.panning { cursor: grabbing; }
  .muted { color: var(--muted); font-size: 12px; }
  .world { position: absolute; left: 0; top: 0; transform-origin: 0 0; }
  .edges { position: absolute; left: -5000px; top: -5000px; width: 10000px; height: 10000px; overflow: visible; pointer-events: none; }
  .edges path { fill: none; stroke: color-mix(in srgb, var(--muted) 70%, transparent); stroke-width: 2; transform: translate(5000px, 5000px); }
  .edges path.on { stroke: #1d9e75; stroke-width: 3; }
  .edges path.dim { stroke-dasharray: 5 5; opacity: .45; }
  .mid { position: absolute; transform: translate(-50%, -50%); display: flex; flex-direction: column; align-items: center; gap: 2px; }
  .elabel { font-size: 11px; padding: 0 6px; border-radius: 8px; background: var(--surface-solid, var(--bg)); color: var(--muted); border: 1px solid var(--border); }
  .elabel.on { color: #1d9e75; border-color: #1d9e75; }
  .plus { width: 22px; height: 22px; padding: 0; justify-content: center; border-radius: 50%; font-size: 12px; opacity: .55; background: var(--surface-solid, var(--bg)); }
  .plus:hover, .plus.target { opacity: 1; border-color: var(--accent); }
  .plus.target { transform: scale(1.4); box-shadow: 0 0 0 3px color-mix(in srgb, var(--accent) 30%, transparent); }
  .node {
    --hue: #888780;
    position: absolute; display: grid; align-content: start; gap: 3px; padding: 8px 10px 8px 12px; text-align: left; white-space: normal;
    border-radius: 10px; border: 1px solid color-mix(in srgb, var(--hue) 45%, var(--border)); border-left: 5px solid var(--hue);
    background: var(--surface-solid, var(--bg)); color: var(--text); cursor: pointer; overflow: hidden;
  }
  .node.trig { --hue: #7f77dd; }
  .node.cond { --hue: #e0a12a; }
  .node.act { --hue: #1d9e75; }
  .node.neutral { --hue: #888780; }
  .node:hover { border-color: var(--hue); }
  .node.sel { box-shadow: 0 0 0 3px color-mix(in srgb, var(--accent) 45%, transparent); }
  .node.dim { opacity: .5; }
  .kind { font-size: 11px; color: color-mix(in srgb, var(--hue) 75%, var(--text)); }
  .label { font-size: 13px; line-height: 1.3; display: -webkit-box; -webkit-line-clamp: 2; line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden; }
  .mark { position: absolute; top: 6px; right: 6px; width: 18px; height: 18px; border-radius: 50%; display: grid; place-items: center; font-size: 11px; }
  .mark.ok { background: color-mix(in srgb, #1d9e75 25%, transparent); color: #1d9e75; }
  .mark.no { background: color-mix(in srgb, var(--bad) 25%, transparent); color: var(--bad-text, var(--bad)); }
  .join { position: absolute; width: 64px; height: 64px; border-radius: 50%; padding: 0; justify-content: center; font-weight: 600; font-size: 13px; color: var(--accent-text, var(--accent)); background: var(--surface-solid, var(--bg)); border: 2px solid color-mix(in srgb, #7f77dd 60%, transparent); }
  .endnode { position: absolute; width: 44px; height: 44px; border-radius: 50%; padding: 0; justify-content: center; font-size: 18px; border: 1px dashed var(--border); background: var(--surface-solid, var(--bg)); color: var(--muted); }
  .palette { position: absolute; left: 10px; top: 10px; width: 150px; display: grid; gap: 4px; padding: 8px; border: 1px solid var(--border); border-radius: 10px; background: var(--surface-solid, var(--bg)); cursor: default; }
  .palette.narrow { width: auto; padding: 5px; }
  .palette.narrow .pitem { justify-content: center; padding: 5px 7px; font-size: 15px; }
  .ptitle { font-size: 11px; text-transform: uppercase; letter-spacing: .06em; color: var(--muted); padding: 0 4px 2px; }
  .pitem { --hue: #888780; justify-content: flex-start; gap: 6px; font-size: 13px; padding: 5px 8px; border-left: 3px solid var(--hue); cursor: grab; }
  .pitem.trig { --hue: #7f77dd; }
  .pitem.cond { --hue: #e0a12a; }
  .pitem.act { --hue: #1d9e75; }
  .controls { position: absolute; left: 180px; bottom: 10px; display: flex; gap: 4px; align-items: center; padding: 4px; border: 1px solid var(--border); border-radius: 10px; background: var(--surface-solid, var(--bg)); cursor: default; }
  /* The zoom bar sits in the middle of the free area (between the side bar and the panel). */
  .controls.middle { transform: translateX(-50%); }
  /* In a narrow canvas the zoom bar sits in the middle below the drawing, all of it visible. */
  .controls.center { left: 50%; transform: translateX(-50%); max-width: calc(100% - 12px); flex-wrap: nowrap; white-space: nowrap; }
  .minimap { position: absolute; right: 10px; bottom: 10px; border: 1px solid var(--border); border-radius: 8px; background: var(--surface-solid, var(--bg)); cursor: pointer; }
  .minimap.left { right: 380px; }
  .minimap rect { fill: color-mix(in srgb, var(--muted) 60%, transparent); }
  .minimap rect.trig { fill: #7f77dd; }
  .minimap rect.cond { fill: #e0a12a; }
  .minimap rect.act { fill: #1d9e75; }
  .minimap rect.vp { fill: color-mix(in srgb, var(--accent) 12%, transparent); stroke: var(--accent); stroke-width: 1.5; }
  .plus.moving { opacity: .9; transform: scale(1.25); }
  .zoom { font-size: 12px; min-width: 44px; text-align: center; color: var(--muted); }
  .legend { position: absolute; left: 180px; top: 10px; display: flex; gap: 6px; align-items: center; font-size: 12px; padding: 4px 10px; border-radius: 10px; background: var(--surface-solid, var(--bg)); border: 1px solid var(--border); cursor: default; }
  .ln { display: inline-block; width: 22px; height: 3px; background: #1d9e75; border-radius: 2px; }
  .bad { color: var(--bad-text, var(--bad)); }
  .inspector { position: absolute; top: 10px; right: 10px; bottom: 10px; width: 340px; display: none; align-content: start; gap: 8px; padding: 12px; overflow-y: auto; border: 1px solid var(--accent); border-radius: 10px; background: var(--surface-solid, var(--bg)); cursor: default; user-select: text; }
  .inspector.open { display: grid; }
  .ihead { display: flex; gap: 6px; align-items: center; }
  .grow { flex: 1; }
  .inspector.sheet { left: 6px; right: 6px; width: auto; top: auto; bottom: 6px; max-height: 65%; box-shadow: 0 -6px 24px rgba(0, 0, 0, .35); }
  .sheetbtn { position: absolute; top: 8px; right: 8px; font-size: 13px; padding: 6px 12px; border-radius: 999px; background: var(--surface-solid, var(--bg)); border: 1px solid var(--accent); }
</style>
