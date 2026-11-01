// A small, safe Markdown renderer for AI answers: the text is escaped first,
// then a few things are turned into HTML (bold, italic, inline code, links,
// headings, lists). Code blocks are split off so the chat can show them with
// their own buttons.

const esc = (s) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');

function inline(s) {
  return esc(s)
    .replace(/`([^`\n]+)`/g, '<code>$1</code>')
    .replace(/\*\*([^*\n]+)\*\*/g, '<b>$1</b>')
    .replace(/(^|[\s(])\*([^*\n]+)\*(?=[\s).,!?:;]|$)/g, '$1<i>$2</i>')
    .replace(/\[([^\]\n]+)\]\((https?:\/\/[^\s)]+)\)/g, '<a href="$2" target="_blank" rel="noopener noreferrer">$1</a>')
    .replace(/(^|[\s(])(https?:\/\/[^\s<)]+)/g, '$1<a href="$2" target="_blank" rel="noopener noreferrer">$2</a>');
}

function blockHtml(text) {
  const out = [];
  let list = null; // 'ul' | 'ol'
  const close = () => {
    if (list) out.push(`</${list}>`);
    list = null;
  };
  for (const line of text.split('\n')) {
    let m;
    if ((m = /^\s*[-*•]\s+(.*)$/.exec(line))) {
      if (list !== 'ul') { close(); out.push('<ul>'); list = 'ul'; }
      out.push(`<li>${inline(m[1])}</li>`);
    } else if ((m = /^\s*\d+[.)]\s+(.*)$/.exec(line))) {
      if (list !== 'ol') { close(); out.push('<ol>'); list = 'ol'; }
      out.push(`<li>${inline(m[1])}</li>`);
    } else if ((m = /^(#{1,4})\s+(.*)$/.exec(line))) {
      close();
      out.push(`<p class="h">${inline(m[2])}</p>`);
    } else if (!line.trim()) {
      close();
      out.push('<p class="gap"></p>');
    } else {
      close();
      out.push(`<p>${inline(line)}</p>`);
    }
  }
  close();
  return out.join('');
}

/** Splits an answer into text parts (as safe HTML) and code blocks. */
export function renderMarkdown(text) {
  const parts = [];
  const re = /```([\w.+-]*)[^\n]*\n([\s\S]*?)(```|$)/g;
  let last = 0;
  let m;
  while ((m = re.exec(text))) {
    if (m.index > last) parts.push({ kind: 'text', html: blockHtml(text.slice(last, m.index)) });
    parts.push({ kind: 'code', lang: m[1] || '', code: m[2].replace(/\n$/, '') });
    last = re.lastIndex;
  }
  if (last < text.length) parts.push({ kind: 'text', html: blockHtml(text.slice(last)) });
  return parts;
}
