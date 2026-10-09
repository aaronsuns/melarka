// Who owns the lock screen, the car's controls and the Media Session right
// now. Music, channel episodes, an episode's video and a preview each have
// their own media element; whichever starts playing claims the session and
// every other player pauses itself when it hears the claim. This is what
// keeps episodes from ever mixing with music.
export type SessionOwner = "music" | "episode" | "video" | "preview";

let owner: SessionOwner = "music";
const listeners = new Set<(o: SessionOwner) => void>();

// force: tell the listeners even when o already owns it — music really
// sounding must make any stale episode/video/preview UI step aside.
export function claimSession(o: SessionOwner, opts: { force?: boolean } = {}): void {
  if (owner === o && !opts.force) return;
  owner = o;
  listeners.forEach((fn) => fn(o));
}

export function ownsSession(o: SessionOwner): boolean {
  return owner === o;
}

export function onSessionClaim(fn: (o: SessionOwner) => void): () => void {
  listeners.add(fn);
  return () => void listeners.delete(fn);
}

/** Tests only: back to the start state. */
export function resetSessionOwner(): void {
  owner = "music";
}
