import { useEffect, useRef, useState } from "react";
import { api } from "../api/client";
import { useAuth } from "../auth/AuthProvider";
import { Cover, coverUrl } from "../components/Cover";
import { EditTags } from "../components/EditTags";
import { EditTrack } from "../components/EditTrack";
import { LyricsPicker } from "../components/LyricsPicker";
import { duration, qualityLabel } from "../format";
import { errorMessage } from "../i18n/errors";
import { useT } from "../i18n/i18n";
import { RepeatIcon, RepeatOneIcon, ShuffleIcon } from "../components/icons";
import type { Lyrics } from "../api/types";
import { hasLyrics, LyricStrip, LyricsView, syncedLines } from "./Lyrics";
import { usePlayer, usePlayerProgress } from "./PlayerProvider";
import { upcoming } from "./queue";

const DELETE_ARM_MS = 5000;

// The last explicit cover/lyrics choice on this device. "cover" always opens
// on the cover; "lyrics" (and no choice yet) opens on the lyrics whenever the
// track has some. Storage may be unavailable (private mode, blocked site
// data): then it's simply not remembered.
type NowView = "cover" | "lyrics";
const NOW_VIEW_KEY = "lark.nowView";
function readNowView(): NowView | null {
  try {
    const v = localStorage.getItem(NOW_VIEW_KEY);
    return v === "cover" || v === "lyrics" ? v : null;
  } catch {
    return null;
  }
}
function saveNowView(v: NowView) {
  try {
    localStorage.setItem(NOW_VIEW_KEY, v);
  } catch {
    // not remembered; this session still follows the choice
  }
}

export function NowPlaying({ onClose, lyrics, reloadLyrics }: { onClose: () => void; lyrics: Lyrics | null; reloadLyrics: () => void }) {
  const t = useT();
  const p = usePlayer();
  const progress = usePlayerProgress();
  const { user } = useAuth();
  const [showQueue, setShowQueue] = useState(false);
  // Keyed by track id, not a bare boolean: an in-flight favorite request
  // that resolves after the user has already skipped to another track must
  // not paint its result onto whatever track happens to be current by
  // then — keying the override lets a stale result simply not apply
  // (rather than needing to race a ref check at render time), and it also
  // means flipping back to a track you already acted on remembers it.
  const [favoriteOverride, setFavoriteOverride] = useState<{ id: number; value: boolean } | null>(null);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [editing, setEditing] = useState(false);
  const [editingTags, setEditingTags] = useState(false);
  const [view, setView] = useState<NowView | null>(readNowView);
  // Tapping the cover of a track without lyrics still opens the lyrics view
  // (it says there are none) — for that track only.
  const [forcedLyricsId, setForcedLyricsId] = useState<number | null>(null);
  // While the new track's lyrics load, keep whichever view was up, so a
  // track change doesn't flash the cover between two songs with lyrics; on
  // open, the lyrics unless the stored choice is the cover.
  const lastShowLyrics = useRef(readNowView() !== "cover");
  const [pickingLyrics, setPickingLyrics] = useState(false);
  const [msg, setMsg] = useState("");
  const [scrub, setScrub] = useState<number | null>(null);
  const busy = useRef(false);
  const currentId = useRef<number | null>(null);
  const deleteTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const sliderRef = useRef<HTMLInputElement>(null);
  const scrubEndTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const track = p.current;

  function disarmDelete() {
    setConfirmDelete(false);
    if (deleteTimer.current) {
      clearTimeout(deleteTimer.current);
      deleteTimer.current = null;
    }
  }

  function armDelete() {
    setConfirmDelete(true);
    if (deleteTimer.current) clearTimeout(deleteTimer.current);
    deleteTimer.current = setTimeout(() => setConfirmDelete(false), DELETE_ARM_MS);
  }

  // A new track means any in-flight state from the previous one (an armed
  // delete confirmation, an error message, a scrub-in-progress position, a
  // stuck busy guard from a request that never settled) no longer applies.
  useEffect(() => {
    currentId.current = track?.id ?? null;
    setConfirmDelete(false);
    setEditing(false);
    setEditingTags(false);
    setPickingLyrics(false);
    setMsg("");
    setScrub(null);
    busy.current = false;
    return () => {
      if (deleteTimer.current) {
        clearTimeout(deleteTimer.current);
        deleteTimer.current = null;
      }
    };
  }, [track?.id]);

  // The seek slider previews continuously via onInput while dragging (cheap:
  // local state only), but only commits — and issues the actual range
  // request — once the gesture ends, via a native "change" listener
  // attached directly to the element. A React onChange prop won't do here:
  // it's driven by React's controlled-input value-change tracking, which
  // for a range input fires on every "input" event, i.e. continuously
  // during the drag (the whole thing this is trying to avoid).
  useEffect(() => {
    const el = sliderRef.current;
    if (!el) return;
    const commit = (e: Event) => {
      p.seek(Number((e.target as HTMLInputElement).value));
      setScrub(null);
    };
    el.addEventListener("change", commit);
    return () => el.removeEventListener("change", commit);
  }, [p]);
  useEffect(
    () => () => {
      if (scrubEndTimer.current) clearTimeout(scrubEndTimer.current);
    },
    [],
  );

  // A drag that ends where it started fires no "change", so the preview
  // would stay frozen. Drop it once the gesture ends — on the next tick, so
  // a "change" dispatched by the same release still commits the dragged
  // value first (clearing synchronously would re-render the controlled
  // input back to the old position before "change" reads it).
  function endScrub() {
    if (scrubEndTimer.current) clearTimeout(scrubEndTimer.current);
    scrubEndTimer.current = setTimeout(() => {
      scrubEndTimer.current = null;
      setScrub(null);
    }, 0);
  }

  // Opening Now Playing is a fresh look: a miss is asked again (the server's
  // background prefetch may have found them meanwhile). Found lyrics are
  // cached, so nothing is refetched for them.
  useEffect(() => {
    if (lyrics && !lyrics.found) reloadLyrics();
  }, []);

  if (!track) return null;
  let showLyrics: boolean;
  if (forcedLyricsId === track.id) showLyrics = true;
  else if (!lyrics) showLyrics = lastShowLyrics.current;
  else showLyrics = view !== "cover" && hasLyrics(lyrics);
  lastShowLyrics.current = showLyrics;
  const stripLines = !showLyrics && !showQueue ? syncedLines(lyrics) : null;

  function showCover() {
    saveNowView("cover");
    setView("cover");
    setForcedLyricsId(null);
  }
  function openLyrics(id: number) {
    saveNowView("lyrics");
    setView("lyrics");
    setForcedLyricsId(id);
    if (lyrics && !lyrics.found) reloadLyrics();
  }
  const fav = favoriteOverride && favoriteOverride.id === track.id ? favoriteOverride.value : track.favorite;
  const pos = scrub ?? progress.position;
  const queueUpcoming = upcoming(p.queue);

  async function run(id: number, fn: () => Promise<void>) {
    if (busy.current) return;
    busy.current = true;
    setMsg("");
    try {
      await fn();
    } catch (e) {
      // Only surface the error if the user is still looking at the track
      // it happened for — otherwise it'd read as a failure of whatever
      // they've since moved on to.
      if (currentId.current === id) setMsg(errorMessage(e, "common.actionFailed"));
    } finally {
      busy.current = false;
    }
  }

  return (
    <div className="now" role="dialog" aria-label={t("now.ariaLabel")}>
      <div className="now-top">
        <button className="icon" aria-label={t("now.close")} onClick={onClose}>⌄</button>
        <span className="badge">{qualityLabel(track)} · {p.quality === "lossless" ? t("now.lossless") : p.quality === "high" ? t("now.high") : t("now.saver")}</span>
        <span className="now-top-end">
          {showLyrics && !showQueue && (
            <button className="icon" aria-label={t("now.cover")} onClick={showCover}>▣</button>
          )}
          <button className="icon" aria-label={t("now.queue")} aria-pressed={showQueue} onClick={() => setShowQueue((s) => !s)}>☰</button>
        </span>
      </div>
      {showQueue ? (
        <ul className="now-queue">
          {queueUpcoming.map((q, i) => (
            <li key={`${p.queue.index + 1 + i}-${q.id}`}>
              <button className="row" onClick={() => p.jump(p.queue.index + 1 + i)}>
                <span className="ellipsis">{q.title}</span><span className="muted small ellipsis">{q.artist}</span>
              </button>
            </li>
          ))}
          {queueUpcoming.length === 0 && <li className="muted">{p.modes.repeat === "all" ? t("now.repeatAllEnd") : t("now.radioEnd")}</li>}
        </ul>
      ) : showLyrics ? (
        <LyricsView key={track.id} lyrics={lyrics} trackId={track.id} onEmptied={() => setForcedLyricsId(track.id)} />
      ) : (
        <button className="now-art" aria-label={t("now.lyrics")} onClick={() => openLyrics(track.id)}>
          <Cover seed={track.album || track.title} label={track.title} size={320} src={coverUrl("track", track.id, 1000)} />
        </button>
      )}
      <div className="now-meta">
        <h2 className="ellipsis">{track.title}</h2>
        <p className="ellipsis muted">{[track.artist || t("common.unknownArtist"), track.album].filter(Boolean).join(" · ")}</p>
      </div>
      {stripLines && <LyricStrip key={track.id} lines={stripLines} offset={lyrics?.offset_ms ?? 0} />}
      {(p.error || msg) && <p className="error">{p.error || msg}</p>}
      <input
        ref={sliderRef}
        type="range"
        aria-label={t("now.position")}
        min={0}
        max={Math.max(1, Math.round(progress.duration))}
        step={1}
        value={Math.round(pos)}
        onInput={(e) => setScrub(Number((e.target as HTMLInputElement).value))}
        onPointerUp={endScrub}
        onTouchEnd={endScrub}
        onBlur={endScrub}
      />
      <div className="now-times muted small"><span>{duration(pos * 1000)}</span><span>-{duration(Math.max(0, progress.duration - pos) * 1000)}</span></div>
      <div className="now-controls">
        {p.modesAvailable && (
          <button className="icon mode" aria-label={t("now.shuffle")} aria-pressed={p.modes.shuffle} onClick={() => p.setShuffle(!p.modes.shuffle)}>
            <ShuffleIcon size={22} />
          </button>
        )}
        <button className="icon big" aria-label={t("common.previous")} onClick={p.prev}>⏮</button>
        <button className="icon huge" aria-label={p.playing ? t("common.pause") : t("common.play")} onClick={p.toggle}>{p.playing ? "⏸" : "▶"}</button>
        <button className="icon big" aria-label={t("common.next")} onClick={p.next}>⏭</button>
        {p.modesAvailable && (
          <button
            className="icon mode"
            aria-label={t(`now.repeat.${p.modes.repeat}`)}
            aria-pressed={p.modes.repeat !== "off"}
            data-repeat={p.modes.repeat}
            onClick={p.cycleRepeat}
          >
            {p.modes.repeat === "one" ? <RepeatOneIcon size={22} /> : <RepeatIcon size={22} />}
          </button>
        )}
      </div>
      <div className="now-actions">
        <button
          className="icon"
          aria-label={fav ? t("common.unfavorite") : t("common.favorite")}
          onClick={() => run(track.id, async () => { await api.setFavorite(track.id, !fav); setFavoriteOverride({ id: track.id, value: !fav }); })}
        >
          {fav ? "♥" : "♡"}
        </button>
        <button className="icon" aria-label={t("common.notForMe")} onClick={() => run(track.id, async () => { await api.setDislike(track.id, true); p.remove(track.id); })}>👎</button>
        {user?.role === "admin" && (
          <button className="icon" aria-label={t("now.editInfo")} onClick={() => setEditing(true)}>✎</button>
        )}
        {user?.role === "admin" && (
          <button className="icon" aria-label={t("track.editTags")} onClick={() => setEditingTags(true)}>🏷</button>
        )}
        {user?.role === "admin" && (
          <button className="icon" aria-label={t("lyrics.change")} onClick={() => setPickingLyrics(true)}>🎤</button>
        )}
        {user?.role === "admin" &&
          (confirmDelete ? (
            <button className="secondary danger" onClick={() => run(track.id, async () => { await api.trashTrack(track.id); p.remove(track.id); disarmDelete(); })}>{t("now.confirmDelete")}</button>
          ) : (
            <button className="icon" aria-label={t("now.deleteSong")} onClick={armDelete}>🗑</button>
          ))}
      </div>
      {editingTags && <EditTags track={track} onClose={() => setEditingTags(false)} />}
      {pickingLyrics && <LyricsPicker track={track} onClose={() => setPickingLyrics(false)} onChanged={reloadLyrics} />}
      {editing && (
        <EditTrack
          track={track}
          onClose={() => setEditing(false)}
          onSaved={(updated) => {
            p.updateTrack(updated);
            setEditing(false);
          }}
        />
      )}
    </div>
  );
}
