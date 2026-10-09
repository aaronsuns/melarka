import { useSyncExternalStore } from "react";

// Whether the server has Channels on (GET /info channels; channels.enabled).
// Assumed on until /info answers otherwise: an older server without the
// field, or no answer, keeps the 频道 tab.
let enabled = true;
const listeners = new Set<() => void>();

export function setChannelsEnabled(on: boolean) {
  if (on === enabled) return;
  enabled = on;
  listeners.forEach((fn) => fn());
}

function subscribe(fn: () => void) {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

export function useChannelsEnabled(): boolean {
  return useSyncExternalStore(subscribe, () => enabled);
}
