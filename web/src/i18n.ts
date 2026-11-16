import { createElement, Fragment, type ReactNode } from 'react';

// Interface translations. English text is the key, so anything without a translation
// shows in English. Catalogs load before the app starts, so t() is a plain function.

export const languages = [
  ['en', 'English'], ['de', 'Deutsch'], ['fr', 'Français'], ['nl', 'Nederlands'], ['es', 'Español'],
] as const;
export type Language = typeof languages[number][0];

const catalogs = import.meta.glob<{ default: Record<string, string> }>('./locales/*.json');
const storageKey = 'pastime-language';
let messages: Record<string, string> = {};
export let locale: Language = 'en';

function supported(value: string | null | undefined): Language | null {
  const code = (value ?? '').toLowerCase().split('-')[0];
  return languages.some(([l]) => l === code) ? code as Language : null;
}

/** The chosen language, or the browser's first supported one. */
export function preferredLanguage(): Language {
  let saved: string | null = null;
  try { saved = localStorage.getItem(storageKey); } catch { /* storage unavailable */ }
  return supported(saved) ?? navigator.languages.map(supported).find(Boolean) ?? 'en';
}

export async function loadLocale(): Promise<void> {
  locale = preferredLanguage();
  document.documentElement.lang = locale;
  if (locale === 'en') return;
  const load = catalogs[`./locales/${locale}.json`];
  if (load) messages = (await load()).default;
}

/** Switches the interface language; the page reloads so every screen follows. */
export function setLanguage(value: Language | 'auto') {
  try {
    if (value === 'auto') localStorage.removeItem(storageKey); else localStorage.setItem(storageKey, value);
  } catch { return; /* Reloading cannot apply a choice that was not stored. */ }
  if (languageSetting() !== value) return;
  window.location.reload();
}

/** The stored choice, or 'auto' when following the browser. */
export function languageSetting(): Language | 'auto' {
  try { return supported(localStorage.getItem(storageKey)) ?? 'auto'; } catch { return 'auto'; }
}

/** Translates English text. {name} placeholders are filled from vars. */
export function t(text: string, vars?: Record<string, string | number>): string {
  const out = messages[text] || text;
  return vars ? out.replace(/\{(\w+)\}/g, (match, key: string) => key in vars ? String(vars[key]) : match) : out;
}

/** Picks the singular or plural sentence for n and fills {n}. */
export function plural(n: number, one: string, other: string, vars?: Record<string, string | number>): string {
  return t(n === 1 ? one : other, { n: n.toLocaleString(locale), ...vars });
}

/** Translates a sentence whose {placeholders} are elements, such as a link or <code>. */
export function tx(text: string, vars: Record<string, ReactNode>): ReactNode {
  const parts = t(text).split(/\{(\w+)\}/);
  return createElement(Fragment, null, ...parts.map((part, i) => i % 2 ? (vars[part] ?? `{${part}}`) : part));
}
