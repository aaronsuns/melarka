import { duration, qualityLabel, coverStyle, initials, formatSize, popularity } from "./format";
import { setLocale } from "./i18n/i18n";
import type { Track } from "./api/types";

const t = (codec: string, bitrate: number, lossless: boolean) => ({ codec, bitrate, lossless }) as Track;

test("duration", () => {
  expect(duration(0)).toBe("0:00");
  expect(duration(215_000)).toBe("3:35");
  expect(duration(3_725_000)).toBe("1:02:05");
});

test("qualityLabel", () => {
  expect(qualityLabel(t("flac", 900, true))).toBe("FLAC");
  expect(qualityLabel(t("pcm_s16le", 1411, true))).toBe("WAV");
  expect(qualityLabel(t("mp3", 320, false))).toBe("320k MP3");
  expect(qualityLabel(t("wmav2", 128, false))).toBe("128k WMA");
  expect(qualityLabel(t("aac", 0, false))).toBe("AAC");
});

test("coverStyle is deterministic and initials handle Chinese", () => {
  expect(coverStyle("甜蜜蜜")).toEqual(coverStyle("甜蜜蜜"));
  expect(coverStyle("a").background).not.toEqual(coverStyle("b").background);
  expect(initials("甜蜜蜜")).toBe("甜");
  expect(initials("  alan walker")).toBe("A");
  expect(initials("")).toBe("♪");
});

test("formatSize", () => {
  const MB = 1024 * 1024;
  expect(formatSize(0)).toBe("0 B");
  expect(formatSize(2048)).toBe("2 KB");
  expect(formatSize(6 * MB)).toBe("6 MB");
  expect(formatSize(500 * MB)).toBe("500 MB");
  expect(formatSize(1024 * MB)).toBe("1 GB");
  expect(formatSize(1536 * MB)).toBe("1.5 GB");
});

test("popularity: compact counts in the UI language, nothing when unknown", () => {
  expect(popularity(354_205, 4_000)).toBe("35.4万次播放 · 4000赞"); // the suite runs in zh-Hans
  expect(popularity(234_239_467, 0)).toBe("2.3亿次播放");
  expect(popularity(0, 0)).toBe("");
  expect(popularity(undefined, undefined)).toBe("");
  setLocale("en");
  expect(popularity(354_205, 4_000)).toBe("354.2K views · 4K likes");
  expect(popularity(1, 1)).toBe("1 view · 1 like");
  setLocale("zh-Hans");
});
