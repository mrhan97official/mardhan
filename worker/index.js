// Merged into the generated service worker by next-pwa (customWorkerDir).
// Shows Web Push notifications even when DevControl is closed, and opens the
// related page when a notification is tapped.

self.addEventListener("push", (event) => {
  let data = {};
  try {
    data = event.data ? event.data.json() : {};
  } catch (_) {
    data = { body: event.data ? event.data.text() : "" };
  }
  const title = data.title || "DevControl";
  const options = {
    body: data.body || "",
    icon: "/api/branding/icon?size=192",
    data: { url: typeof data.url === "string" && data.url.startsWith("/") ? data.url : "/" },
  };
  if (data.tag) {
    options.tag = data.tag;
    options.renotify = true;
  }
  event.waitUntil(self.registration.showNotification(title, options));
});

self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  const path = (event.notification.data && event.notification.data.url) || "/";
  const target = new URL(path, self.location.origin).href;
  event.waitUntil((async () => {
    const windows = await self.clients.matchAll({ type: "window", includeUncontrolled: true });
    for (const client of windows) {
      if (new URL(client.url).origin !== self.location.origin) continue;
      await client.focus();
      if ("navigate" in client) {
        try { await client.navigate(target); } catch (_) { /* uncontrolled tab: focus is enough */ }
      }
      return;
    }
    await self.clients.openWindow(target);
  })());
});
