import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { api } from "../api/client";
import type { OnOpen, Prefs } from "../api/types";
import { setUserLanguage } from "../i18n/i18n";

export type { OnOpen, Prefs };

interface PrefsState {
  prefs: Prefs;
  loaded: boolean;
  save(p: Prefs): Promise<void>;
}

const Ctx = createContext<PrefsState | null>(null);

// FALLBACK is what a signed-in user sees while GET /me/preferences hasn't
// answered yet, or if it never will (network down at boot): behave like
// the default before shuffling favorites on open: always resume.
const FALLBACK: Prefs = { language: null, on_open: "resume" };

export function PrefsProvider({ children }: { children: ReactNode }) {
  const [prefs, setPrefs] = useState<Prefs>(FALLBACK);
  const [loaded, setLoaded] = useState(false);

  useEffect(() => {
    let alive = true;
    api
      .preferences()
      .then((p) => {
        if (!alive) return;
        setPrefs(p);
        setUserLanguage(p.language);
      })
      .catch(() => {})
      .finally(() => alive && setLoaded(true));
    return () => {
      alive = false;
      setUserLanguage(null); // signed out / another user: back to the server default
    };
  }, []);

  const save = useCallback(async (p: Prefs) => {
    const saved = await api.savePreferences(p);
    setPrefs(saved);
    setUserLanguage(saved.language);
  }, []);

  const value = useMemo(() => ({ prefs, loaded, save }), [prefs, loaded, save]);
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function usePrefs(): PrefsState {
  const v = useContext(Ctx);
  if (!v) throw new Error("usePrefs outside PrefsProvider");
  return v;
}
