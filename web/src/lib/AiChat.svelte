<script>
  // The AI chat: write back and forth with the AI the admin set up, add files
  // (text, PDF, Word, pictures) for it to work with, switch web results on,
  // and save or copy what it writes. On the desktop either a large window
  // with the earlier chats on the left (layout "window", the default) or a
  // sidebar the dashboard moves aside for ("side"); full screen on phones.
  // Chats are kept in this browser, per account.
  import { post } from './api.js';
  import { t, i18n } from './i18n.svelte.js';
  import { toast } from './toast.svelte.js';
  import { renderMarkdown } from './markdown.js';
  import { confirmDialog } from './dialog.svelte.js';

  // ai: {label, local, web, dashboard}; start: a first question (from the search) with
  // the hits that were shown, sent right away.
  let { ai, start = null, layout = 'window', userId = 0, onClose } = $props();

  // svelte-ignore state_referenced_locally
  const KEY = `heimdeck-ai-chats-${userId}`;
  const MAX_CHATS = 30;
  let chats = $state(loadChats()); // [{id, title, at, messages}], newest first
  let currentId = $state('');
  let current = $derived(chats.find((c) => c.id === currentId));
  let messages = $derived(current?.messages || []);
  let history = $state(false); // sidebar layout: the list of chats as a menu
  let text = $state('');
  let files = $state([]); // {name, kind: 'text'|'image', text?, data?}
  let busy = $state(false);
  let web = $state(false);
  // 📋: the AI gets what the tiles show. Remembered in this browser.
  let dash = $state(readDash());
  function readDash() {
    try { return localStorage.getItem('heimdeck-ai-dashboard') === '1'; } catch { return false; }
  }
  function toggleDash() {
    dash = !dash;
    try { localStorage.setItem('heimdeck-ai-dashboard', dash ? '1' : '0'); } catch { /* private window */ }
  }
  let hits = $state([]); // results from the search, sent along with the conversation
  let list;
  let input;
  let picker;
  let abort;

  function loadChats() {
    try {
      const list = JSON.parse(localStorage.getItem(KEY) || '[]');
      return Array.isArray(list) ? list : [];
    } catch {
      return [];
    }
  }
  function keep() {
    if (current) {
      current.at = Date.now();
      current.title = (current.messages.find((m) => m.role === 'user')?.shown || current.messages[0]?.files?.[0] || t('Neuer Chat')).slice(0, 60);
    }
    chats.sort((a, b) => b.at - a.at);
    if (chats.length > MAX_CHATS) chats.length = MAX_CHATS;
    try {
      // Pictures are not kept – they would fill the storage.
      localStorage.setItem(KEY, JSON.stringify(chats.map((c) => ({ ...c, messages: c.messages.map((m) => ({ ...m, images: [], actions: undefined })) }))));
    } catch {
      /* private mode or full */
    }
  }
  function openChat(id) {
    stop();
    currentId = id;
    history = false;
    hits = [];
    scroll();
    input?.focus();
  }
  function removeChat(id) {
    const i = chats.findIndex((c) => c.id === id);
    if (i >= 0) chats.splice(i, 1);
    if (currentId === id) currentId = '';
    keep();
  }
  const when = (at) => {
    const d = new Date(at);
    return d.toDateString() === new Date().toDateString()
      ? d.toLocaleTimeString(i18n.lang === 'de' ? 'de-DE' : 'en-GB', { hour: '2-digit', minute: '2-digit' })
      : d.toLocaleDateString(i18n.lang === 'de' ? 'de-DE' : 'en-GB', { day: 'numeric', month: 'short' });
  };

  // The sidebar leaves the dashboard usable next to it.
  let narrow = $state(matchMedia('(max-width: 640px)').matches);
  $effect(() => {
    const docked = layout === 'side' && !narrow;
    document.body.classList.toggle('chat-docked', docked);
    return () => document.body.classList.remove('chat-docked');
  });

  const scroll = () => queueMicrotask(() => list?.scrollTo({ top: list.scrollHeight }));

  $effect(() => {
    input?.focus();
    if (start?.question) {
      hits = start.hits || [];
      text = start.question;
      send();
    }
  });

  const TEXT_EXT = /\.(txt|md|markdown|csv|tsv|json|xml|ya?ml|ini|conf|cfg|toml|log|html?|css|js|ts|py|go|sh|bat|ps1|sql|env|properties|srt|vtt)$/i;
  const MAX_TEXT = 200_000;

  async function readImage(file) {
    const bitmap = await createImageBitmap(file, { imageOrientation: 'from-image' });
    const scale = Math.min(1, 1568 / Math.max(bitmap.width, bitmap.height));
    const c = document.createElement('canvas');
    c.width = Math.round(bitmap.width * scale);
    c.height = Math.round(bitmap.height * scale);
    c.getContext('2d').drawImage(bitmap, 0, 0, c.width, c.height);
    return c.toDataURL('image/jpeg', 0.85);
  }
  const toBase64 = (file) =>
    new Promise((res, rej) => {
      const r = new FileReader();
      r.onload = () => res(String(r.result).split(',')[1]);
      r.onerror = () => rej(r.error);
      r.readAsDataURL(file);
    });

  // PDFs are read by pdf.js (Mozilla's PDF reader), loaded only when needed.
  // Scanned PDFs have no text: then the first pages go along as pictures.
  async function readPdf(file) {
    const pdfjs = await import('pdfjs-dist');
    pdfjs.GlobalWorkerOptions.workerSrc = (await import('pdfjs-dist/build/pdf.worker.min.mjs?url')).default;
    const task = pdfjs.getDocument({ data: await file.arrayBuffer(), isEvalSupported: false });
    const doc = await task.promise;
    let text = '';
    for (let n = 1; n <= doc.numPages && text.length < MAX_TEXT; n++) {
      const page = await doc.getPage(n);
      const content = await page.getTextContent();
      text += (n > 1 ? `\n\n— ${t('Seite')} ${n} —\n` : '') + content.items.map((it) => it.str + (it.hasEOL ? '\n' : '')).join('');
    }
    const images = [];
    if (text.replace(/\s|—|\d/g, '').length < 20 * Math.min(doc.numPages, 5)) {
      for (let n = 1; n <= Math.min(doc.numPages, 4); n++) {
        const page = await doc.getPage(n);
        const base = page.getViewport({ scale: 1 });
        const viewport = page.getViewport({ scale: Math.min(2, 1568 / Math.max(base.width, base.height)) });
        const canvas = document.createElement('canvas');
        canvas.width = Math.round(viewport.width);
        canvas.height = Math.round(viewport.height);
        await page.render({ canvas, canvasContext: canvas.getContext('2d'), viewport }).promise;
        images.push(canvas.toDataURL('image/jpeg', 0.85));
      }
      text = '';
    }
    const pages = doc.numPages;
    task.destroy();
    return { text: text.slice(0, MAX_TEXT), images, pages };
  }

  async function addFiles(list) {
    for (const f of list) {
      try {
        if (f.type.startsWith('image/')) {
          files.push({ name: f.name, kind: 'image', data: await readImage(f) });
        } else if (f.type === 'application/pdf' || /\.pdf$/i.test(f.name)) {
          const pdf = await readPdf(f);
          if (pdf.text) files.push({ name: f.name, kind: 'text', text: pdf.text });
          else if (pdf.images.length) {
            toast(t('„{name}“ enthält keinen Text (gescannt?) – die ersten Seiten gehen als Bilder mit; das versteht nur ein Modell, das sehen kann.', { name: f.name }));
            pdf.images.forEach((data, i) => files.push({ name: `${f.name} (${t('Seite')} ${i + 1})`, kind: 'image', data }));
          }
        } else if (/\.docx$/i.test(f.name)) {
          const { text } = await post('/ai/extract', { name: f.name, data: await toBase64(f) });
          files.push({ name: f.name, kind: 'text', text: text.slice(0, MAX_TEXT) });
        } else if (f.type.startsWith('text/') || TEXT_EXT.test(f.name) || f.type === 'application/json') {
          const content = await f.text();
          if (content.length > MAX_TEXT) toast(t('„{name}“ ist sehr lang – nur der Anfang geht mit.', { name: f.name }));
          files.push({ name: f.name, kind: 'text', text: content.slice(0, MAX_TEXT) });
        } else {
          toast(t('„{name}“: diese Art von Datei geht noch nicht – Text, Markdown, CSV, JSON, PDF, Word und Bilder gehen.', { name: f.name }), 'bad');
        }
      } catch (err) {
        toast(`${f.name}: ${err.message}`, 'bad');
      }
    }
  }
  function onPick(e) {
    addFiles([...(e.currentTarget.files || [])]);
    e.currentTarget.value = '';
  }
  function onDrop(e) {
    e.preventDefault();
    addFiles([...(e.dataTransfer?.files || [])]);
  }
  function onPaste(e) {
    const pics = [...(e.clipboardData?.files || [])].filter((f) => f.type.startsWith('image/'));
    if (pics.length) {
      e.preventDefault();
      addFiles(pics);
    }
  }

  async function send() {
    const q = text.trim();
    if ((!q && !files.length) || busy) return;
    // Files go into the message as text; pictures as pictures (for models that can see).
    if (!current) {
      chats.unshift({ id: Date.now().toString(36) + Math.random().toString(36).slice(2, 6), title: '', at: Date.now(), messages: [] });
      currentId = chats[0].id;
    }
    let body = q;
    for (const f of files.filter((f) => f.kind === 'text')) body += `\n\n---\n${t('Datei')} „${f.name}“:\n\`\`\`\n${f.text}\n\`\`\``;
    messages.push({
      role: 'user', text: body, shown: q, files: files.map((f) => f.name), images: files.filter((f) => f.kind === 'image').map((f) => f.data),
    });
    text = '';
    files = [];
    const reply = { role: 'assistant', text: '', web: 0, dash: 0 };
    messages.push(reply);
    const msg = messages[messages.length - 1];
    busy = true;
    scroll();
    const ctrl = (abort = new AbortController());
    try {
      const res = await fetch('/api/ai/chat', {
        method: 'POST', signal: ctrl.signal, credentials: 'same-origin',
        headers: { 'Content-Type': 'application/json', 'X-Lang': i18n.lang },
        body: JSON.stringify({
          messages: messages.slice(0, -1).map((m) => ({ role: m.role, text: m.text, images: m.images || [] })),
          hits, web, dashboard: !!(ai.dashboard && dash),
          date: new Date().toLocaleString(i18n.lang === 'de' ? 'de-DE' : 'en-GB', { weekday: 'long', day: 'numeric', month: 'long', year: 'numeric', hour: '2-digit', minute: '2-digit' }),
        }),
      });
      if (!res.ok) throw new Error((await res.json().catch(() => null))?.error || t('Fehler {code}', { code: res.status }));
      msg.web = Number(res.headers.get('X-Web-Results')) || 0;
      msg.dash = Number(res.headers.get('X-Dashboard-Tiles')) || 0;
      msg.webVia = res.headers.get('X-Web-Via') || '';
      const webErr = res.headers.get('X-Web-Error');
      if (webErr) msg.webError = decodeURIComponent(webErr);
      if (res.headers.get('X-AI-Tools') === 'fallback') msg.noTools = true;
      const reader = res.body.getReader();
      const dec = new TextDecoder();
      // Between the text come events, framed by \x1e … \x1f: a tool the AI
      // used, or an action it proposes as a button.
      let pending = '';
      for (;;) {
        const { done, value } = await reader.read();
        if (done) break;
        pending += dec.decode(value, { stream: true });
        for (;;) {
          const i = pending.indexOf('\x1e');
          if (i < 0) {
            msg.text += pending;
            pending = '';
            break;
          }
          msg.text += pending.slice(0, i);
          const j = pending.indexOf('\x1f', i);
          if (j < 0) {
            pending = pending.slice(i);
            break;
          }
          try { onEvent(msg, JSON.parse(pending.slice(i + 1, j))); } catch { /* broken event */ }
          pending = pending.slice(j + 1);
        }
        scroll();
      }
      if (!msg.text.trim()) msg.error = t('Die KI hat keine Antwort gegeben.');
    } catch (err) {
      msg.error = err.name === 'AbortError' ? t('Abgebrochen') : err.message;
    } finally {
      busy = false;
      keep();
      input?.focus();
    }
  }

  function onEvent(msg, ev) {
    // Assign first, then push through the reactive array – pushing into the
    // value of `msg.x ||= []` would miss Svelte's proxy the first time.
    if (ev.tool) {
      if (!msg.tools) msg.tools = [];
      msg.tools.push({ tool: ev.tool, arg: ev.arg || '' });
    }
    if (ev.propose) {
      if (!msg.actions) msg.actions = [];
      msg.actions.push({ ...ev.propose, reason: ev.reason || '', state: '' });
    }
  }
  const toolText = (x) =>
    x.tool === 'list' ? t('Kacheln angesehen') : x.tool === 'read' ? t('{name} gelesen', { name: x.arg }) : t('gesucht: „{q}“', { q: x.arg });

  // A proposed action runs only when clicked – through the tile's normal
  // action, with the same rights as its buttons on the dashboard.
  async function runAction(a) {
    if (a.state === 'busy' || a.state === 'done') return;
    if (a.confirm && !(await confirmDialog(a.confirm, { confirmLabel: a.label, danger: a.tone === 'danger' }))) return;
    a.state = 'busy';
    try {
      await post(`/tiles/${a.tileId}/action`, { action: a.action, params: a.params || {} });
      a.state = 'done';
      toast(t('{label} – erledigt', { label: a.label }));
    } catch (e) {
      a.state = '';
      toast(e.message, 'bad');
    }
  }

  function stop() {
    abort?.abort();
  }
  function reset() {
    stop();
    currentId = '';
    history = false;
    hits = [];
    input?.focus();
  }

  async function copy(s) {
    try {
      await navigator.clipboard.writeText(s);
      toast(t('Kopiert'));
    } catch {
      toast(t('Kopieren nicht möglich – bitte von Hand markieren.'), 'bad');
    }
  }
  const EXT = { markdown: 'md', md: 'md', python: 'py', py: 'py', javascript: 'js', js: 'js', json: 'json', yaml: 'yml', yml: 'yml', csv: 'csv', html: 'html', xml: 'xml', bash: 'sh', sh: 'sh', go: 'go', sql: 'sql', css: 'css', ts: 'ts', txt: 'txt', text: 'txt', ini: 'ini', toml: 'toml' };
  function save(content, lang = 'md') {
    const ext = EXT[lang.toLowerCase()] || 'txt';
    const a = document.createElement('a');
    a.href = URL.createObjectURL(new Blob([content], { type: 'text/plain;charset=utf-8' }));
    a.download = `heimdeck-ki.${ext}`;
    a.click();
    setTimeout(() => URL.revokeObjectURL(a.href), 1000);
  }

  function onKey(e) {
    if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
      e.preventDefault();
      send();
    }
  }
  function onWindowKey(e) {
    if (e.key === 'Escape') {
      e.preventDefault();
      busy ? stop() : onClose();
    }
  }
</script>

<svelte:window onkeydown={onWindowKey} onresize={() => (narrow = matchMedia('(max-width: 640px)').matches)} />

{#snippet chatList()}
  {#each chats as c (c.id)}
    <div class="chat-item" class:on={c.id === currentId}>
      <button class="ghost pick" onclick={() => openChat(c.id)} title={c.title}><span class="ct">{c.title || t('Neuer Chat')}</span><span class="muted cw">{when(c.at)}</span></button>
      <button class="ghost del" onclick={() => removeChat(c.id)} aria-label={t('Chat löschen')} title={t('Chat löschen')}>✕</button>
    </div>
  {:else}
    <p class="muted small none">{t('Noch keine Chats.')}</p>
  {/each}
{/snippet}

<div class="chat-backdrop {layout}" class:narrow role="presentation" onclick={(e) => e.target === e.currentTarget && layout !== 'side' && onClose()}>
  <div class="chat card {layout}" class:narrow role="dialog" aria-modal={layout !== 'side'} aria-label={t('KI-Chat')} ondragover={(e) => e.preventDefault()} ondrop={onDrop}>
    {#if layout === 'window' && !narrow}
      <aside class="chats">
        <button class="small primary new" onclick={reset}>＋ {t('Neuer Chat')}</button>
        <div class="chat-list">{@render chatList()}</div>
        <p class="muted tiny keep-note">{t('Chats bleiben in diesem Browser gespeichert.')}</p>
      </aside>
    {/if}
    <div class="main">
    <header>
      <span class="title">✨ {ai.label}</span>
      <span class="muted where">{ai.local ? t('läuft bei dir') : t('Cloud-Anbieter')}</span>
      <span class="spacer"></span>
      {#if ai.dashboard}
        <button class="small toggle" class:on={dash} aria-pressed={dash} onclick={toggleDash} title={t('Die KI bekommt mit, was deine Kacheln gerade zeigen')}>📋 {t('Dashboard')}</button>
      {/if}
      {#if ai.web}
        <button class="small toggle" class:on={web} aria-pressed={web} onclick={() => (web = !web)} title={t('Die KI bekommt Ergebnisse aus der Websuche dazu')}>🌐 {t('Web')}</button>
      {/if}
      {#if layout !== 'window' || narrow}
        <div class="hist">
          <button class="small" class:on={history} onclick={() => (history = !history)} aria-expanded={history} title={t('Frühere Chats')}>🕘</button>
          {#if history}<div class="hist-menu card">{@render chatList()}</div>{/if}
        </div>
        <button class="small" onclick={reset} disabled={!messages.length}>＋ {t('Neuer Chat')}</button>
      {/if}
      <button class="ghost small close" onclick={onClose} aria-label={t('Schließen')}>✕</button>
    </header>

    <div class="messages" bind:this={list}>
      {#if !messages.length}
        <div class="empty">
          <p class="big">✨</p>
          <p>{t('Frag etwas – oder gib der KI eine Datei, die sie zusammenfassen, übersetzen oder umschreiben soll.')}</p>
          <p class="muted small">{t('Dateien per 📎, Ziehen oder Einfügen. Text, Markdown, CSV, JSON, PDF, Word (.docx) und Bilder.')}{ai.web ? ' ' + t('🌐 holt Ergebnisse aus dem Web dazu.') : ''}</p>
        </div>
      {/if}
      {#each messages as m, i (i)}
        {#if m.role === 'user'}
          <div class="msg user">
            {#if m.shown ?? m.text}<div class="bubble">{m.shown ?? m.text}</div>{/if}
            {#if m.files?.length}<div class="file-chips">{#each m.files as f}<span class="badge">📄 {f}</span>{/each}</div>{/if}
          </div>
        {:else}
          <div class="msg ai">
            {#if m.error}
              <p class="bad">{m.error}</p>
            {:else if !m.text}
              {#if m.tools?.length}<p class="tools muted small">🔧 {m.tools.map(toolText).join(' · ')} …</p>{/if}
              <p class="muted">{t('Denkt nach …')}</p>
            {:else}
              {#if m.tools?.length}<p class="tools muted small">🔧 {m.tools.map(toolText).join(' · ')}</p>{/if}
              {#each renderMarkdown(m.text) as part}
                {#if part.kind === 'code'}
                  <div class="code">
                    <div class="code-bar"><span>{part.lang || t('Text')}</span><span class="spacer"></span>
                      <button class="ghost small" onclick={() => copy(part.code)}>{t('Kopieren')}</button>
                      <button class="ghost small" onclick={() => save(part.code, part.lang)}>⬇ {t('Speichern')}</button>
                    </div>
                    <pre><code>{part.code}</code></pre>
                  </div>
                {:else}
                  <div class="md">{@html part.html}</div>
                {/if}
              {/each}
            {/if}
            {#if m.actions?.length}
              <div class="proposals">
                {#each m.actions as a}
                  <button class="small {a.tone === 'danger' ? 'danger' : 'primary'}" disabled={a.state === 'busy' || a.state === 'done'} title={a.reason} onclick={() => runAction(a)}>
                    {a.state === 'done' ? '✓' : '▶'} {a.label} <span class="ptile">· {a.tile}</span>
                  </button>
                {/each}
              </div>
            {/if}
            {#if m.text && !(busy && i === messages.length - 1)}
              <div class="msg-actions">
                {#if m.noTools}<span class="muted small" title={t('Dieses Modell kann keine Werkzeuge benutzen – es hat stattdessen die ganze Übersicht bekommen.')}>📋 {t('ohne Werkzeuge')}</span>{/if}
                {#if m.dash}<span class="muted small">{t('📋 {n} Kacheln als Kontext', { n: m.dash })}</span>{/if}
                {#if m.web}<span class="muted small">🌐 {t('{n} Web-Ergebnisse', { n: m.web })}</span>{/if}
                {#if m.webVia}<span class="muted small">🌐 {t('mit Websuche über {name}', { name: m.webVia })}</span>{/if}
                {#if m.webError}<span class="web-error small">⚠ {t('Websuche:')} {m.webError}</span>{/if}
                <span class="spacer"></span>
                <button class="ghost small" onclick={() => copy(m.text)}>{t('Kopieren')}</button>
                <button class="ghost small" onclick={() => save(m.text)}>⬇ {t('Speichern')}</button>
              </div>
            {/if}
          </div>
        {/if}
      {/each}
    </div>

    <footer>
      {#if files.length}
        <div class="file-chips">
          {#each files as f, i (i)}
            <span class="badge">{f.kind === 'image' ? '🖼' : '📄'} {f.name}<button class="ghost x" onclick={() => files.splice(i, 1)} aria-label={t('Entfernen')}>✕</button></span>
          {/each}
        </div>
      {/if}
      <div class="compose">
        <button class="ghost attach" onclick={() => picker.click()} title={t('Datei anhängen')} aria-label={t('Datei anhängen')}>📎</button>
        <input type="file" multiple hidden bind:this={picker} onchange={onPick} />
        <textarea bind:this={input} bind:value={text} onkeydown={onKey} onpaste={onPaste} rows="1" placeholder={t('Nachricht an {name} …', { name: ai.label })}></textarea>
        {#if busy}
          <button class="small" onclick={stop}>■ {t('Stopp')}</button>
        {:else}
          <button class="small primary" onclick={send} disabled={!text.trim() && !files.length}>{t('Senden')}</button>
        {/if}
      </div>
      <p class="muted tiny">{t('Enter sendet, Shift+Enter neue Zeile.')} {hits.length ? t('{n} Suchtreffer gehen als Kontext mit.', { n: hits.length }) : ''}</p>
    </footer>
    </div>
  </div>
</div>

<style>
  /* Large window (default): centred, earlier chats on the left. */
  .chat-backdrop { position: fixed; inset: 0; z-index: 90; background: rgba(0, 0, 0, .5); display: flex; align-items: center; justify-content: center; padding: 24px; }
  .chat { width: min(1060px, 100%); height: min(780px, calc(100vh - 48px)); display: flex; background: var(--surface-solid); padding: 0; overflow: hidden; }
  .main { flex: 1; min-width: 0; display: flex; flex-direction: column; }
  .chats { width: 240px; flex: none; border-right: 1px solid var(--border); display: flex; flex-direction: column; gap: 8px; padding: 10px; background: color-mix(in srgb, var(--surface-2) 60%, transparent); }
  .chats .new { justify-content: center; }
  .chat-list { flex: 1; overflow-y: auto; display: grid; gap: 2px; align-content: start; }
  .keep-note { text-align: center; }
  .chat-item { display: flex; align-items: center; border-radius: var(--r-sm); }
  .chat-item:hover, .chat-item.on { background: var(--surface-2); }
  .chat-item.on { box-shadow: inset 3px 0 0 var(--accent); }
  .pick { flex: 1; min-width: 0; display: grid; text-align: left; padding: 6px 8px; border: 0; background: none; }
  .ct { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 13.5px; }
  .cw { font-size: 11.5px; }
  .del { opacity: 0; padding: 0 6px; font-size: 11px; }
  .chat-item:hover .del, .chat-item.on .del { opacity: .7; }
  .none { padding: 8px; }
  .hist { position: relative; }
  .hist-menu { position: absolute; right: 0; top: calc(100% + 6px); width: 280px; max-height: 60vh; overflow-y: auto; z-index: 5; padding: 6px; display: grid; gap: 2px; background: var(--surface-solid); }
  /* Sidebar: full height on the right, no dark backdrop – the dashboard moves aside and stays usable. */
  .chat-backdrop.side:not(.narrow) { background: none; padding: 0; pointer-events: none; justify-content: flex-end; align-items: stretch; }
  .chat.side:not(.narrow) { pointer-events: auto; width: 440px; height: 100vh; border-radius: 0; border-top: 0; border-bottom: 0; border-right: 0; }
  :global(body.chat-docked) { padding-right: 440px; }
  header { display: flex; align-items: center; gap: 8px; padding: 10px 12px; border-bottom: 1px solid var(--border); }
  .title { font-weight: 600; }
  .where { font-size: 12px; }
  .spacer { flex: 1; }
  .toggle.on { background: color-mix(in srgb, var(--accent) 18%, transparent); border-color: var(--accent); color: var(--accent); }
  .close { width: 32px; height: 32px; padding: 0; justify-content: center; }
  .messages { flex: 1; overflow-y: auto; padding: 14px; display: flex; flex-direction: column; gap: 12px; }
  .empty { margin: auto; text-align: center; max-width: 380px; display: grid; gap: 6px; }
  .empty .big { font-size: 34px; margin: 0; }
  .empty p { margin: 0; }
  .msg { display: grid; gap: 6px; max-width: 92%; }
  .msg.user { align-self: flex-end; justify-items: end; }
  .bubble { background: color-mix(in srgb, var(--accent) 18%, var(--surface-2)); border-radius: 14px 14px 4px 14px; padding: 8px 12px; white-space: pre-wrap; font-size: 14px; }
  .msg.ai { align-self: flex-start; font-size: 14px; line-height: 1.55; }
  .md :global(p) { margin: 0; }
  .md :global(p.gap) { height: 8px; }
  .md :global(p.h) { font-weight: 700; margin-top: 4px; }
  .md :global(ul), .md :global(ol) { margin: 4px 0; padding-left: 20px; }
  .md :global(code) { font-family: ui-monospace, monospace; font-size: 12.5px; background: var(--surface-2); padding: 1px 4px; border-radius: 4px; }
  .md :global(a) { color: var(--accent); }
  .code { border: 1px solid var(--border); border-radius: var(--r-sm); overflow: hidden; margin: 4px 0; }
  .code-bar { display: flex; align-items: center; gap: 4px; padding: 2px 6px 2px 10px; background: var(--surface-2); font-size: 12px; color: var(--muted); }
  pre { margin: 0; padding: 10px; overflow-x: auto; font-size: 12.5px; max-height: 360px; }
  .msg-actions { display: flex; align-items: center; gap: 4px; }
  .file-chips { display: flex; flex-wrap: wrap; gap: 6px; }
  .file-chips .badge { display: inline-flex; align-items: center; gap: 4px; max-width: 240px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .x { padding: 0 4px; font-size: 11px; }
  footer { border-top: 1px solid var(--border); padding: 10px 12px; display: grid; gap: 6px; }
  .compose { display: flex; gap: 6px; align-items: flex-end; }
  textarea { flex: 1; resize: none; min-height: 38px; max-height: 160px; field-sizing: content; font: inherit; font-size: 14px; }
  .attach { font-size: 18px; width: 38px; height: 38px; padding: 0; justify-content: center; }
  .tiny { font-size: 11.5px; margin: 0; }
  .small { font-size: 12.5px; }
  .web-error { color: var(--warn); line-height: 1.4; }
  .tools { margin: 0 0 6px; }
  .proposals { display: flex; flex-wrap: wrap; gap: 6px; margin: 8px 0 2px; }
  .ptile { opacity: .75; font-weight: 400; }
  @media (max-width: 640px) {
    .chat-backdrop { padding: 0; }
    .chat { width: 100%; height: 100%; height: 100dvh; border-radius: 0; }
    .hist-menu { width: min(300px, calc(100vw - 24px)); }
    .msg { max-width: 100%; }
  }
</style>
