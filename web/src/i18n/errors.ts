import { ApiError } from "../api/client";
import { has, t } from "./i18n";

// errorMessage turns a caught value into text to show the user. An ApiError
// whose code we recognise (has an `error.<code>` key) is translated; any
// other Error shows its own message as-is (the server's English text, e.g.
// for a code we don't special-case, or a status-derived generic code); a
// non-Error value (should not normally happen) falls back to fallbackKey.
export function errorMessage(e: unknown, fallbackKey: string): string {
  if (e instanceof ApiError && e.code && has(`error.${e.code}`)) return t(`error.${e.code}`);
  if (e instanceof Error) return e.message;
  return t(fallbackKey);
}

// jobErrorText renders a download job's stored error. Lark's own worker
// writes tokens of the form "lark:<code>" (see job.error.* keys); anything
// else is yt-dlp's own last output line, shown as-is.
export function jobErrorText(error: string): string {
  const prefix = "lark:";
  if (error.startsWith(prefix)) return t(`job.error.${error.slice(prefix.length)}`);
  return error;
}

// jobErrorShort: a short, translated line for a failed download's row. yt-dlp's
// own output (English, long: "ERROR: [youtube] …: HTTP Error 403: Forbidden")
// is never shown as is: the row says what kind of failure it was, and the
// caller keeps the raw text for a tooltip.
export function jobErrorShort(error: string): string {
  if (error.startsWith("lark:")) return jobErrorText(error);
  if (/HTTP Error (403|429)|Forbidden|Too Many Requests|Sign in to confirm/i.test(error)) return t("job.short.refused");
  if (/unavailable|Private video|has been removed|not available|members-only|copyright/i.test(error)) return t("job.short.unavailable");
  if (/timed? ?out|Connection|Network|Errno|Temporary failure|Name or service/i.test(error)) return t("job.short.network");
  return t("job.short.failed");
}

const YT_SEARCH_FAILED_PREFIX = "YouTube search failed: ";

// searchErrorText: a failed YouTube search (videos or channels). The server's
// youtube_search_failed carries yt-dlp's own reason after a fixed English
// prefix; that reason is shown inside the translated sentence.
export function searchErrorText(e: unknown): string {
  if (e instanceof ApiError && e.code === "youtube_search_failed") {
    const reason = e.message.startsWith(YT_SEARCH_FAILED_PREFIX) ? e.message.slice(YT_SEARCH_FAILED_PREFIX.length) : e.message;
    return t("yt.searchFailed", { reason });
  }
  return errorMessage(e, "yt.searchFailedGeneric");
}

// previewErrorText: a preview request that failed. YouTube pushing back
// (503 preview_retry) says when to try again, from the server's Retry-After.
// Anything without a code we know (a network error, a bare 5xx, a 404 for a
// preview that expired) is just "couldn't load the preview".
export function previewErrorText(e: unknown): string {
  if (e instanceof ApiError && e.code === "preview_retry" && e.retryAfter) return t("preview.retryIn", { seconds: e.retryAfter });
  if (e instanceof ApiError && e.code && has(`error.${e.code}`)) return t(`error.${e.code}`);
  return t("preview.failed");
}

// isTransient: worth asking again by itself — the request never reached the
// server (fetch threw) or the server failed without saying why (5xx, no code).
export function isTransient(e: unknown): boolean {
  if (!(e instanceof ApiError)) return true;
  return !e.code && e.status >= 500;
}

// failedPreviewText: a preview row stored as failed. Its error is one of the
// server's own codes (video_preview_unavailable, preview_too_long) or
// yt-dlp's last line, which is not shown.
export function failedPreviewText(error: string): string {
  if ((error === "video_preview_unavailable" || error === "preview_too_long") && has(`error.${error}`)) return t(`error.${error}`);
  return t("preview.failed");
}
