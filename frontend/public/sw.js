// Service worker: показывает push-уведомления о заявках и открывает нужную страницу по клику.
// Отдаётся с корня сайта (/sw.js), см. View.ServiceWorkerHandler.

self.addEventListener('install', () => self.skipWaiting())
self.addEventListener('activate', (event) => event.waitUntil(self.clients.claim()))

self.addEventListener('push', (event) => {
  let data = {}
  try {
    data = event.data ? event.data.json() : {}
  } catch {
    data = { body: event.data ? event.data.text() : '' }
  }
  event.waitUntil(
    self.registration.showNotification(data.title || 'Поддержка', {
      body: data.body || '',
      tag: data.tag || undefined, // одинаковый tag заменяет прежнее уведомление, а не копит новые
      data: { url: data.url || '/support' },
    }),
  )
})

self.addEventListener('notificationclick', (event) => {
  event.notification.close()
  // Открываем только страницы нашего сайта, что бы ни пришло в данных.
  let target = new URL('/support', self.location.origin)
  try {
    const u = new URL(event.notification.data?.url || '/support', self.location.origin)
    if (u.origin === self.location.origin) target = u
  } catch {
    /* оставляем /support */
  }
  event.waitUntil(
    self.clients.matchAll({ type: 'window', includeUncontrolled: true }).then((windows) => {
      for (const w of windows) {
        if (new URL(w.url).origin === self.location.origin && 'focus' in w) {
          return w.navigate(target.href).then((c) => (c || w).focus())
        }
      }
      return self.clients.openWindow(target.href)
    }),
  )
})
