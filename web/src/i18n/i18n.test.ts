import { afterEach, expect, test } from "vitest";
import { dictionaries, setLocale, setServerDefault, setUserLanguage, getLocale, t } from "./i18n";

afterEach(() => setLocale("zh-Hans"));

test("params, plurals and English fallback", () => {
  dictionaries.en["test.hello"] = "Hello {name}";
  dictionaries.en["test.songs_one"] = "{count} song";
  dictionaries.en["test.songs_other"] = "{count} songs";
  dictionaries.sv["test.songs_one"] = "{count} låt";
  dictionaries.sv["test.songs_other"] = "{count} låtar";
  dictionaries["zh-Hans"]["test.songs_other"] = "{count} 首";
  setLocale("en");
  expect(t("test.hello", { name: "Alice" })).toBe("Hello Alice");
  expect(t("test.songs", { count: 1 })).toBe("1 song");
  expect(t("test.songs", { count: 3 })).toBe("3 songs");
  setLocale("sv");
  expect(t("test.songs", { count: 1 })).toBe("1 låt");
  expect(t("test.hello", { name: "A" })).toBe("Hello A"); // sv lacks it → en
  setLocale("zh-Hans");
  expect(t("test.songs", { count: 1 })).toBe("1 首");
  expect(t("test.absent")).toBe("test.absent");
  for (const k of Object.keys(dictionaries.en)) if (k.startsWith("test.")) delete dictionaries.en[k];
});

test("user choice beats server default; null falls back to it", () => {
  setServerDefault("sv");
  expect(getLocale()).toBe("sv");
  setUserLanguage("zh-Hant");
  expect(getLocale()).toBe("zh-Hant");
  setUserLanguage(null);
  expect(getLocale()).toBe("sv");
  setServerDefault("bogus"); // ignored
  expect(getLocale()).toBe("sv");
  setServerDefault(null);
});
