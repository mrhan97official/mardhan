"use client";

const channelName = "devcontrol-data-changed";

export function notifyDataChanged() {
  window.dispatchEvent(new Event(channelName));
  if (typeof BroadcastChannel !== "undefined") {
    try {
      const channel = new BroadcastChannel(channelName);
      channel.postMessage(Date.now());
      channel.close();
      return;
    } catch {
      // Fall back to storage events when BroadcastChannel is unavailable.
    }
  }
  try {
    localStorage.setItem(channelName, String(Date.now()));
  } catch {
    // Browser storage can be disabled; the current tab still refreshes.
  }
}

export function subscribeDataChanges(reload: () => void): () => void {
  window.addEventListener(channelName, reload);
  if (typeof BroadcastChannel !== "undefined") {
    try {
      const channel = new BroadcastChannel(channelName);
      channel.onmessage = reload;
      return () => { window.removeEventListener(channelName, reload); channel.close(); };
    } catch {
      // Fall back to storage events when BroadcastChannel is unavailable.
    }
  }
  const onStorage = (event: StorageEvent) => { if (event.key === channelName) reload(); };
  window.addEventListener("storage", onStorage);
  return () => {
    window.removeEventListener(channelName, reload);
    window.removeEventListener("storage", onStorage);
  };
}
