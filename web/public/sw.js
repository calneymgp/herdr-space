// Migration worker for the Force Agent PWA formerly served at this origin.
// Keep this registration so old browsers can update /sw.js in place. With no
// fetch handler, every HERDR request goes to the network, including HTML/API.
self.addEventListener('install', event => {
  event.waitUntil(self.skipWaiting())
})

self.addEventListener('message', event => {
  if (event.data && event.data.type === 'SKIP_WAITING') self.skipWaiting()
})

self.addEventListener('activate', event => {
  event.waitUntil((async () => {
    const scope = self.registration.scope
    const names = await caches.keys()
    await Promise.all(names
      .filter(name => name === 'opencode-assets' || (name.startsWith('workbox-') && name.endsWith(scope)))
      .map(name => caches.delete(name)))
    await self.clients.claim()
  })())
})
