// Minimal no-op service worker.
//
// It is registered at app boot from src/main.tsx so the PWA install hooks work,
// but it deliberately does no caching. Replace this with real
// caching/offline/push logic when you need it.

self.addEventListener('install', () => {
  self.skipWaiting();
});

self.addEventListener('activate', event => {
  event.waitUntil(self.clients.claim());
});
