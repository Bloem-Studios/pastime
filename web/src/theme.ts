// Theme preference: dark ("Midnight") is the default, light is its inversion, and
// system follows the operating system. public/theme.js applies it before first paint.
export type ThemePreference = 'dark' | 'light' | 'system';
const key = 'pastime-theme';

export function readTheme(): ThemePreference {
  try {
    const value = localStorage.getItem(key);
    return value === 'light' || value === 'system' ? value : 'dark';
  } catch {
    return 'dark';
  }
}

export function applyTheme(preference: ThemePreference) {
  const dark = preference === 'system' ? matchMedia('(prefers-color-scheme: dark)').matches : preference === 'dark';
  document.documentElement.dataset.theme = dark ? 'dark' : 'light';
}

/** Saves and applies a preference. Returns false when the browser refuses storage. */
export function saveTheme(preference: ThemePreference): boolean {
  applyTheme(preference);
  try {
    localStorage.setItem(key, preference);
    return true;
  } catch {
    return false;
  }
}
