import { useState } from "react";
import { errorMessage } from "../i18n/errors";
import { useT } from "../i18n/i18n";
import { usePlayer } from "../player/PlayerProvider";

/** The favorites shuffle action and its error line (player.shuffleFavorites primes in the tap). */
export function useShuffleFavorites() {
  const player = usePlayer();
  const [msg, setMsg] = useState("");
  async function go() {
    setMsg("");
    try {
      await player.shuffleFavorites();
    } catch (e) {
      setMsg(errorMessage(e, "home.shuffleUnavailable"));
    }
  }
  return { go, msg };
}

/** A compact 🔀 随机播放收藏 button (Library → 收藏 header), same style as the Songs tab's shuffle. */
export function ShuffleFavoritesButton() {
  const t = useT();
  const { go, msg } = useShuffleFavorites();
  return (
    <div className="tab-actions">
      <button className="secondary" onClick={go}>🔀 {t("home.shuffleFavorites")}</button>
      {msg && <p className="error small">{msg}</p>}
    </div>
  );
}
