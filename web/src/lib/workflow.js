// Workflows (automations): shared helpers of the list and the flow view.
import { t, i18n } from './i18n.svelte.js';

export const DAYS = [1, 2, 3, 4, 5, 6, 0];
// 7 January 2024 was a Sunday.
export const dayName = (d) => new Date(2024, 0, 7 + d).toLocaleDateString(i18n.lang, { weekday: 'short' });
export const EVENTS = ['Bewegung', 'Person', 'Fahrzeug', 'Tier', 'Klingel', 'Bereich betreten', 'Sabotage'];
export const COOLDOWNS = [5, 15, 60, 360, 1440];

export const blankTrigger = (kind = 'time') => ({ kind, time: '07:00', days: [], tile: 0, for: 2, stat: '', op: '>', value: '', event: '', match: '', prompt: '', every: 60 });
export const WATCH_EVERY = [15, 30, 60, 180, 360, 1440];
export const blankCond = () => ({ kind: 'value', tile: 0, stat: '', op: '>', value: '' });
export const blankStep = (kind = 'notify') => {
  switch (kind) {
    case 'wait':
      return { kind, minutes: 10 };
    case 'if':
      return { kind, if: blankCond(), then: [], else: [] };
    case 'button':
      return { kind, tile: 0, action: '', params: null, confirm: '' };
    case 'scene':
      return { kind, scene: '' };
    default:
      return { kind: 'notify', text: '', report: false, camera: 0 };
  }
};
export const blankWorkflow = () => ({
  name: '', on: true, noAsk: false, all: false, cooldown: 60,
  triggers: [blankTrigger()], only: { from: '', to: '', days: [] }, conds: [], steps: [blankStep()],
});

// A copy with every field the editor shows, from a saved workflow or an AI draft.
export function editable(w) {
  const fill = (s) => {
    const b = blankStep(s.kind);
    const out = { ...b, ...s };
    if (s.kind === 'if') {
      out.if = { ...blankCond(), ...(s.if || {}) };
      out.then = (s.then || []).map(fill);
      out.else = (s.else || []).map(fill);
    }
    return out;
  };
  const c = JSON.parse(JSON.stringify(w));
  return {
    ...blankWorkflow(), ...c,
    triggers: (c.triggers?.length ? c.triggers : [blankTrigger()]).map((x) => ({ ...blankTrigger(x.kind), ...x })),
    only: { from: '', to: '', days: [], ...(c.only || {}) },
    conds: (c.conds || []).map((x) => ({ ...blankCond(), ...x })),
    steps: (c.steps || []).map(fill),
  };
}

// For saving: numbers as numbers, nothing the server does not need.
export function forSave(w) {
  const num = (v) => Number(v) || 0;
  const step = (s) => {
    const out = { ...s, tile: num(s.tile), camera: num(s.camera), minutes: num(s.minutes) };
    if (s.kind === 'if') {
      out.if = { ...s.if, tile: num(s.if.tile) };
      out.then = s.then.map(step);
      out.else = s.else.map(step);
    }
    return out;
  };
  return {
    ...w,
    cooldown: num(w.cooldown) || 60,
    triggers: w.triggers.map((x) => ({ ...x, tile: num(x.tile), for: num(x.for), every: num(x.every) })),
    conds: w.conds.map((x) => ({ ...x, tile: num(x.tile) })),
    steps: w.steps.map(step),
  };
}

function days(list) {
  if (!list?.length || list.length === 7) return '';
  return DAYS.filter((d) => list.includes(d)).map(dayName).join(', ');
}
const tileName = (x, tiles) => x.tileName || tiles?.find((y) => y.id === Number(x.tile))?.name || '…';
const opText = (op) => (op === '<' ? '<' : op === '=' ? '=' : '>');

export function triggerText(tr, tiles) {
  const name = tileName(tr, tiles);
  switch (tr.kind) {
    case 'time':
      return days(tr.days) ? t('{days} um {time}', { days: days(tr.days), time: tr.time }) : t('Täglich um {time}', { time: tr.time });
    case 'down':
      return t('{tile} antwortet {n} Min. nicht', { tile: name, n: tr.for || 2 });
    case 'up':
      return t('{tile} ist wieder erreichbar', { tile: name });
    case 'manual':
      return t('Von Hand gestartet (Szene)');
    case 'ai':
      return t('KI-Wächter: {text}', { text: (tr.prompt || '…').slice(0, 80) });
    case 'camera':
      return `${name}: ${tr.event ? t(tr.event) : t('jedes Ereignis')}`;
    case 'webhook':
      return `${name}${tr.match ? ` „${tr.match}“` : ''}`;
    default:
      return `${name} · ${tr.stat} ${opText(tr.op)} ${tr.value}`;
  }
}
export const triggerIcon = (kind) => ({ time: '🕑', down: '🔴', up: '🟢', value: '📈', camera: '📹', webhook: '📨', ai: '🤖', manual: '🎬' })[kind] || '⚡';

export function condText(c, tiles) {
  const name = tileName(c, tiles);
  if (c.kind === 'online') return t('{tile} ist erreichbar', { tile: name });
  if (c.kind === 'offline') return t('{tile} ist nicht erreichbar', { tile: name });
  return `${name} · ${c.stat || '…'} ${opText(c.op)} ${c.value || '…'}`;
}

export function stepText(s, tiles, scenes) {
  switch (s.kind) {
    case 'button':
      return '▶ ' + (s.label || (tiles?.find((x) => x.id === Number(s.tile))?.actions.find((a) => a.id === s.action)?.label) || t('Knopf wählen …'));
    case 'scene':
      return '🎬 ' + (s.label || scenes?.find((x) => x.id === s.scene)?.name || t('Szene wählen …'));
    case 'wait':
      return '⏳ ' + t('{n} Min. warten', { n: s.minutes || 1 });
    case 'if':
      return '❓ ' + t('Wenn {cond}', { cond: condText(s.if || {}, tiles) });
    default:
      return (s.report ? '📋 ' + t('Lagebericht') : '✉ ' + (s.text || t('Nachricht'))) + (s.camera ? ' + 📹' : '');
  }
}

export const cooldownName = (m) => (m < 60 ? t('{n} Min.', { n: m }) : m === 60 ? t('1 Stunde') : t('{n} Stunden', { n: m / 60 }));
