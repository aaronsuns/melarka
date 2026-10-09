import { useSyncExternalStore } from "react";
import en from "./locales/en.json";
import zhHans from "./locales/zh-Hans.json";
import zhHant from "./locales/zh-Hant.json";
import sv from "./locales/sv.json";

export const LOCALES = ["en", "zh-Hans", "zh-Hant", "sv"] as const;
export type Locale = (typeof LOCALES)[number];
export const LANGUAGE_NAMES: Record<Locale, string> = { en: "English", "zh-Hans": "简体中文", "zh-Hant": "繁體中文", sv: "Svenska" };
export const dictionaries: Record<Locale, Record<string, string>> = { en, "zh-Hans": zhHans, "zh-Hant": zhHant, sv };

export const isLocale = (x: unknown): x is Locale => typeof x === "string" && (LOCALES as readonly string[]).includes(x);

function stored(): Locale | null {
  try {
    const v = localStorage.getItem("lark.lang");
    return isLocale(v) ? v : null;
  } catch {
    return null;
  }
}

let current: Locale = stored() ?? "en";
let serverDefault: Locale | null = null;
let userChoice: Locale | null = null;
const listeners = new Set<() => void>();
const rules = new Map<Locale, Intl.PluralRules>();

export const getLocale = () => current;
export const getServerDefault = () => serverDefault;
export function subscribe(fn: () => void) {
  listeners.add(fn);
  return () => void listeners.delete(fn);
}
export function setLocale(l: Locale) {
  if (typeof document !== "undefined") document.documentElement.lang = l;
  if (l === current) return;
  current = l;
  listeners.forEach((fn) => fn());
}
// Unknown inputs never change anything: before /info answers (or when it
// fails) the last locale this browser used stays in effect.
function apply() {
  const l = userChoice ?? serverDefault ?? current;
  try {
    localStorage.setItem("lark.lang", l);
  } catch {
    /* private mode */
  }
  setLocale(l);
}
export function setServerDefault(l: string | null) {
  serverDefault = isLocale(l) ? l : null;
  apply();
}
export function setUserLanguage(l: string | null) {
  userChoice = isLocale(l) ? l : null;
  apply();
}

export const has = (key: string) => key in dictionaries.en || `${key}_other` in dictionaries.en;

const warned = new Set<string>();
function lookup(key: string): string | undefined {
  const v = dictionaries[current][key] ?? dictionaries.en[key];
  if (v === undefined && import.meta.env.DEV && !warned.has(key)) {
    warned.add(key);
    console.warn(`i18n: missing key ${key}`);
  }
  return v;
}

export function formatNumber(n: number): string {
  return new Intl.NumberFormat(current).format(n);
}

export function t(key: string, params: Record<string, string | number> = {}): string {
  let s: string | undefined;
  if (typeof params.count === "number") {
    let pr = rules.get(current);
    if (!pr) rules.set(current, (pr = new Intl.PluralRules(current)));
    s = lookup(`${key}_${pr.select(params.count)}`) ?? lookup(`${key}_other`);
  }
  s ??= lookup(key) ?? key;
  return s.replace(/\{(\w+)\}/g, (m, name: string) => {
    const v = params[name];
    return v === undefined ? m : typeof v === "number" ? formatNumber(v) : v;
  });
}

export function useT(): typeof t {
  useSyncExternalStore(subscribe, getLocale, getLocale);
  return t;
}
