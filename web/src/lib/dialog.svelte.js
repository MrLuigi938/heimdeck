// In-page replacement for the browser's prompt() and confirm() boxes.
export const dialogs = $state([]);
let next = 1;

function open(opts) {
  return new Promise((resolve) => {
    dialogs.push({ id: next++, resolve, ...opts });
  });
}

// Resolves to true/false.
export function confirmDialog(message, { title = '', confirmLabel = '', danger = false } = {}) {
  return open({ kind: 'confirm', title, message, confirmLabel, danger });
}

// Resolves to the entered text, or null when cancelled.
export function promptDialog(message, { title = '', value = '', placeholder = '', confirmLabel = '' } = {}) {
  return open({ kind: 'prompt', title, message, value, placeholder, confirmLabel });
}

export function close(d, result) {
  const i = dialogs.indexOf(d);
  if (i >= 0) dialogs.splice(i, 1);
  d.resolve(result);
}
