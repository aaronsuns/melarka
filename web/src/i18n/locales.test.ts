import { describe, expect, test } from "vitest";
import { dictionaries, LOCALES } from "./i18n";

const placeholders = (s: string) => [...s.matchAll(/\{(\w+)\}/g)].map((m) => m[1]).sort().join(",");
const base = (k: string) => k.replace(/_(zero|one|two|few|many|other)$/, "");

describe("every locale is complete", () => {
  const en = dictionaries.en;
  const bases = new Set(Object.keys(en).map(base));
  for (const l of LOCALES) {
    test(`${l}: every key, same placeholders`, () => {
      const d = dictionaries[l];
      const cats = new Intl.PluralRules(l).resolvedOptions().pluralCategories;
      const missing: string[] = [];
      const wrong: string[] = [];
      for (const b of bases) {
        const plural = `${b}_other` in en;
        const ref = plural ? en[`${b}_other`] : en[b];
        for (const k of plural ? cats.map((c) => `${b}_${c}`) : [b]) {
          if (!d[k]?.trim()) missing.push(k);
          else if (placeholders(d[k]) !== placeholders(ref)) wrong.push(k);
        }
      }
      expect(missing).toEqual([]);
      expect(wrong).toEqual([]);
    });
    test(`${l}: no keys en lacks`, () => {
      expect(Object.keys(dictionaries[l]).filter((k) => !bases.has(base(k)))).toEqual([]);
    });
  }
});

// Users see the product as Melarka; "Lark" is only the code name.
describe("the brand is Melarka", () => {
  for (const l of LOCALES) {
    test(`${l}: no value says Lark`, () => {
      const hits = Object.entries(dictionaries[l]).filter(([, v]) => /\bLark\b/.test(v)).map(([k]) => k);
      expect(hits).toEqual([]);
    });
  }
});
