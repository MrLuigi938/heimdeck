<script>
  import { renderMarkdown } from './markdown.js';
  import { t } from './i18n.svelte.js';
  import { TEXT_MIN_FONT, HEADING_MIN_FONT } from './infoBlocks.js';

  // A heading, a note or a saying, in simple Markdown. "heading" makes the
  // text as large as the block; "note" looks like a sticky note.
  let { opt, shape } = $props();
  // Code blocks are shown as plain text here, everything else as safe HTML (escaped first).
  const esc = (v) => v.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
  let html = $derived(
    opt.look === 'heading' ? '' : renderMarkdown(opt.text || '').map((p) => (p.kind === 'code' ? `<pre>${esc(p.code)}</pre>` : p.html)).join(''),
  );

  // The text takes the largest size at which it fits the block (--fit, found by
  // halving) – a short note in a big block grows, a long one shrinks – but never
  // below a readable size, and plain text not above 30 px (times the chosen
  // size). The block cannot be made smaller than the text needs at its smallest
  // size (see textRows).
  const MAX_FONT = 30;
  let box = $state();
  let content = $state();
  $effect(() => {
    if (!box || !content) return;
    html; opt.text; opt.look;
    const fit = () => {
      box.style.setProperty('--fit', '1');
      const cs = getComputedStyle(box);
      const ah = box.clientHeight - parseFloat(cs.paddingTop) - parseFloat(cs.paddingBottom);
      const aw = box.clientWidth - parseFloat(cs.paddingLeft) - parseFloat(cs.paddingRight);
      const base = parseFloat(getComputedStyle(content).fontSize) || 14;
      const scale = parseFloat(cs.getPropertyValue('--ib-scale')) || 1;
      const heading = opt.look === 'heading';
      const lo0 = ((heading ? HEADING_MIN_FONT : TEXT_MIN_FONT) * scale) / base;
      const hi0 = heading ? 3 : (MAX_FONT * scale) / base;
      const fits = (f) => {
        box.style.setProperty('--fit', String(f));
        return content.scrollHeight <= ah + 1 && content.scrollWidth <= aw + 1;
      };
      if (!fits(lo0)) return; // even the smallest size does not fit: it stays there, cut at the end
      let lo = lo0;
      let hi = Math.max(lo0, hi0);
      if (fits(hi)) return;
      for (let i = 0; i < 9; i++) {
        const mid = (lo + hi) / 2;
        if (fits(mid)) lo = mid;
        else hi = mid;
      }
      box.style.setProperty('--fit', String(lo));
    };
    fit();
    document.fonts?.ready.then(fit);
    const ro = new ResizeObserver(fit);
    ro.observe(box);
    return () => ro.disconnect();
  });
</script>

<div class="text look-{opt.look} shape-{shape}" bind:this={box}>
  {#if !opt.text}
    <span class="empty">{t('Text in den Block-Einstellungen eingeben')}</span>
  {:else if opt.look === 'heading'}
    <div class="heading" bind:this={content}>{opt.text}</div>
  {:else}
    <div class="md" bind:this={content}>{@html html}</div>
  {/if}
</div>

<style>
  .text { height: 100%; display: flex; flex-direction: column; justify-content: center; padding: clamp(6px, 4cqmin, 16px); box-sizing: border-box; overflow: hidden;
    text-align: var(--ib-align, left); font-size: calc(clamp(12px, min(4.4cqw, 9cqh), 18px) * var(--ib-scale, 1) * var(--fit, 1)); line-height: 1.4; }
  .heading { font-size: calc(max(16px, min(13cqw, 58cqh)) * var(--ib-scale, 1) * var(--fit, 1)); font-weight: 750; line-height: 1.2; overflow: hidden; text-overflow: ellipsis; letter-spacing: -.01em; overflow-wrap: anywhere; }
  .md { overflow: hidden; overflow-wrap: anywhere; min-height: 0; }
  .md :global(p), .md :global(ul), .md :global(ol), .md :global(h1), .md :global(h2), .md :global(h3) { margin: 0 0 .4em; }
  .md :global(h1), .md :global(h2), .md :global(h3) { font-size: 1.15em; }
  .md :global(ul), .md :global(ol) { padding-left: 1.2em; }
  .md :global(a) { color: var(--ib-accent); }
  .look-quote .md { font-style: italic; font-size: 1.12em; }
  .look-quote .md::before { content: '“'; display: block; font-size: 2.4em; line-height: .6; color: var(--ib-accent); font-style: normal; }
  /* A sticky note: yellow paper in every theme, slightly tilted. */
  .look-note { justify-content: flex-start; background: #fff1a1; color: #3b3418; border-radius: 4px; box-shadow: 0 6px 16px rgb(0 0 0 / .22);
    margin: 6px; height: calc(100% - 12px); transform: rotate(-.8deg); text-shadow: none; }
  .look-note .md :global(a) { color: #7a5b00; }
  .empty { opacity: .55; font-size: .9em; }
</style>
