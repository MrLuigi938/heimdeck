// Tiny global toast queue.
export const toasts = $state([]);
let next = 1;

export function toast(text, tone = 'ok') {
  const id = next++;
  toasts.push({ id, text, tone });
  setTimeout(() => {
    const i = toasts.findIndex((t) => t.id === id);
    if (i >= 0) toasts.splice(i, 1);
  }, tone === 'bad' ? 6000 : 3000);
}
