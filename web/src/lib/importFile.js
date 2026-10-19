import { post } from './api.js';
import { toast } from './toast.svelte.js';
import { t } from './i18n.svelte.js';

// Imports a Heimdeck backup or a Heimdall export (Heimdall → Settings → Export)
// into the given dashboard. Returns true when something was imported.
export async function importFile(file, boardId) {
  if (!file) return false;
  try {
    const data = JSON.parse(await file.text());
    const res = await post('/import' + (boardId ? '?board=' + boardId : ''), data);
    if (res.source === 'heimdall') {
      toast(t('{n} Kachel(n) aus Heimdall übernommen, {m} davon mit Live-Daten', { n: res.tiles, m: res.integrations }));
    } else {
      toast(t('{n} Kachel(n) importiert', { n: res.tiles }));
    }
    return true;
  } catch (err) {
    toast(err.message || t('Datei konnte nicht gelesen werden'), 'bad');
    return false;
  }
}
