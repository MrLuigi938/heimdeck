// Translations for the web interface. German is the source language: the
// code calls t('German text') and gets the English version when English is
// selected. {name} placeholders are filled from the second argument.
import { en } from './en.js';

const KEY = 'heimdeck-lang';

function initial() {
  try {
    const saved = localStorage.getItem(KEY);
    if (saved === 'de' || saved === 'en') return saved;
  } catch {
    /* private mode */
  }
  return (navigator.language || 'de').toLowerCase().startsWith('de') ? 'de' : 'en';
}

export const i18n = $state({ lang: initial() });
document.documentElement.lang = i18n.lang;

export function setLang(lang) {
  if (lang !== 'de' && lang !== 'en') return;
  i18n.lang = lang;
  document.documentElement.lang = lang;
  try {
    localStorage.setItem(KEY, lang);
  } catch {
    /* private mode */
  }
}

export function t(text, vars) {
  let out = i18n.lang === 'en' ? (en[text] ?? text) : text;
  if (vars) out = out.replace(/\{(\w+)\}/g, (m, k) => (k in vars ? vars[k] : m));
  return out;
}

// Locale for dates and numbers.
export const locale = () => (i18n.lang === 'en' ? 'en-GB' : 'de-DE');
