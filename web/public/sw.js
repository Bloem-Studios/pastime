// Caches only content-hashed build assets and fonts, which never change under a URL.
// Pages and API responses always come from the network, so nothing personal is kept on
// the device. When a page cannot load at all, a small offline notice is shown.
const CACHE = 'pastime-static-v1';
const OFFLINE = '/offline.html';
const MAX_ENTRIES = 80;

self.addEventListener('install', event => {
  event.waitUntil(caches.open(CACHE).then(cache => cache.add(OFFLINE)).catch(() => {}).then(() => self.skipWaiting()));
});

self.addEventListener('activate', event => {
  event.waitUntil(caches.keys().then(keys => Promise.all(keys.filter(k => k !== CACHE).map(k => caches.delete(k)))).catch(() => {}).then(() => self.clients.claim()));
});

async function trim(cache) {
  const keys = await cache.keys();
  for (const request of keys.slice(0, Math.max(0, keys.length - MAX_ENTRIES))) {
    if (!request.url.endsWith(OFFLINE)) await cache.delete(request);
  }
}

self.addEventListener('fetch', event => {
  const request = event.request;
  if (request.method !== 'GET') return;
  const url = new URL(request.url);
  if (url.origin !== self.location.origin) return;
  if (url.pathname.startsWith('/assets/')) {
    event.respondWith((async () => {
      let cache;
      try { cache = await caches.open(CACHE); const hit = await cache.match(request); if (hit) return hit; } catch { /* optional cache */ }
      const response = await fetch(request);
      if (cache && response.ok) event.waitUntil(cache.put(request, response.clone()).then(() => trim(cache)).catch(() => {}));
      return response;
    })());
    return;
  }
  if (request.mode === 'navigate') {
    event.respondWith(fetch(request).catch(async () => { try { const page = await caches.match(OFFLINE); if (page) return page; } catch { /* optional cache */ } return new Response('Offline. Reconnect and try again.', { status: 503, headers: { 'Content-Type': 'text/plain' } }); }));
  }
});
