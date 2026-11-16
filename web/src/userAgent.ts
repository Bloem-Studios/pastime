// Names the browser and system in a User-Agent string, for telling sessions apart. Pure, so
// the unit tests run it under Node. Product names are not translated.

export type Device = { browser: string; os: string };

const browsers: [RegExp, string][] = [
  [/\bEdg(e|A|iOS)?\//, 'Edge'],
  [/\b(OPR|Opera)\//, 'Opera'],
  [/\bSamsungBrowser\//, 'Samsung Internet'],
  [/\b(Firefox|FxiOS)\//, 'Firefox'],
  [/\b(HeadlessChrome|Chrome|CriOS|Chromium)\//, 'Chrome'],
  [/\bVersion\/[\d.]+.*\bSafari\//, 'Safari'],
];

const systems: [RegExp, string][] = [
  [/\biPhone\b/, 'iPhone'],
  [/\biPad\b/, 'iPad'],
  [/\bAndroid\b/, 'Android'],
  [/\bCrOS\b/, 'ChromeOS'],
  [/\bWindows\b/, 'Windows'],
  [/\bMac OS X\b|\bMacintosh\b/, 'macOS'],
  [/\bLinux\b/, 'Linux'],
];

/** The browser and system, each empty when unknown. Another client names its first product token. */
export function deviceOf(userAgent: string): Device {
  const ua = userAgent.trim();
  if (!ua) return { browser: '', os: '' };
  const browser = browsers.find(([pattern]) => pattern.test(ua))?.[1]
    ?? (ua.startsWith('Mozilla/') ? '' : ua.split(/[\s/]/)[0].slice(0, 40));
  const os = systems.find(([pattern]) => pattern.test(ua))?.[1] ?? '';
  return { browser, os };
}
