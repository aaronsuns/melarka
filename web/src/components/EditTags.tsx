import { useEffect, useState } from "react";
import { api } from "../api/client";
import type { Track, TrackTag, VocabEntry } from "../api/types";
import { errorMessage } from "../i18n/errors";
import { useT } from "../i18n/i18n";
import { tagLabel } from "./TagChips";

// Typing a vocabulary label (in the current language) or slug picks the slug
// and its kind; anything else is a free-form "other" tag, kept as typed.
function resolve(input: string, vocab: VocabEntry[]): TrackTag {
  const v = input.trim();
  const lower = v.toLowerCase();
  const hit = vocab.find((e) => e.slug === lower || tagLabel(e.slug).toLowerCase() === lower);
  return hit ? { name: hit.slug, kind: hit.kind } : { name: v, kind: "other" };
}

export function EditTags({ track, onClose }: { track: Track; onClose: () => void }) {
  const t = useT();
  const [tags, setTags] = useState<TrackTag[] | null>(null);
  const [vocab, setVocab] = useState<VocabEntry[]>([]);
  const [text, setText] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    api.trackTags(track.id).then(setTags).catch((e) => setError(errorMessage(e, "common.actionFailed")));
    // The vocabulary only feeds suggestions; free-form entry works without it.
    api.vocabulary().then(setVocab).catch(() => {});
  }, [track.id]);

  function withPending(list: TrackTag[]): TrackTag[] {
    if (!text.trim()) return list;
    const tag = resolve(text, vocab);
    return list.some((x) => x.name === tag.name) ? list : [...list, tag];
  }

  function add() {
    if (tags === null) return;
    setTags(withPending(tags));
    setText("");
  }

  async function save() {
    if (busy || tags === null) return;
    setBusy(true);
    setError("");
    try {
      await api.setTrackTags(track.id, withPending(tags));
      onClose();
    } catch (e) {
      setError(errorMessage(e, "common.actionFailed"));
      setBusy(false);
    }
  }

  return (
    <div className="sheet-backdrop" onClick={onClose}>
      <div className="sheet" role="dialog" aria-label={t("editTags.title")} onClick={(e) => e.stopPropagation()}>
        <h2 className="ellipsis">{t("editTags.title")} · {track.title}</h2>
        {error && <p className="error" role="alert">{error}</p>}
        <div className="chips">
          {(tags ?? []).map((tag) => (
            <span key={tag.name} className="chip tag-chip">
              {tagLabel(tag.name)}
              <button
                className="tag-remove"
                aria-label={t("editTags.remove", { name: tagLabel(tag.name) })}
                disabled={busy}
                onClick={() => setTags((tags ?? []).filter((x) => x.name !== tag.name))}
              >
                ×
              </button>
            </span>
          ))}
        </div>
        <form className="sheet-new" onSubmit={(e) => { e.preventDefault(); add(); }}>
          <input aria-label={t("editTags.add")} placeholder={t("editTags.add")} list="vocab-tags" value={text} disabled={busy} onChange={(e) => setText(e.target.value)} />
          <datalist id="vocab-tags">
            {vocab.map((e) => <option key={e.slug} value={tagLabel(e.slug)} />)}
          </datalist>
          <button type="submit" disabled={busy || tags === null}>＋</button>
        </form>
        <button className="primary" disabled={busy || tags === null} onClick={() => void save()}>{t("common.save")}</button>
        <button className="sheet-cancel" disabled={busy} onClick={onClose}>{t("common.cancel")}</button>
      </div>
    </div>
  );
}
