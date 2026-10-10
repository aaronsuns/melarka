import { useCallback, useEffect, useState } from "react";
import { onMusicSounded } from "./sessionOwner";

// What last really played on this device, music or a channel episode, kept
// per user so a reload knows it too. 继续收听 is offered only when it was
// the episode: once music has played since, the bar is music's (paused or
// not) and the episode is resumed from 频道.
export type PlayedKind = "music" | "episode";

const key = (userId: number) => `lark.lastPlayed.${userId}`;

export function loadLastPlayed(userId: number): PlayedKind | null {
  try {
    const v = localStorage.getItem(key(userId));
    return v === "music" || v === "episode" ? v : null;
  } catch {
    return null;
  }
}

function saveLastPlayed(userId: number, k: PlayedKind): void {
  try {
    localStorage.setItem(key(userId), k);
  } catch {
    /* private mode or full: this visit still knows */
  }
}

/** For the episode providers: episodePlaying is their own playing state. */
export function useLastPlayed(userId: number, episodePlaying: boolean): PlayedKind | null {
  const [kind, setKind] = useState(() => loadLastPlayed(userId));
  const note = useCallback(
    (k: PlayedKind) => {
      setKind(k);
      saveLastPlayed(userId, k);
    },
    [userId],
  );
  useEffect(() => {
    if (episodePlaying) note("episode");
  }, [episodePlaying, note]);
  useEffect(() => onMusicSounded(() => note("music")), [note]);
  return kind;
}
