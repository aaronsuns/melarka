import { useCallback, useEffect, useRef, useState } from "react";

/** A row action's failure, shown on the row's text (the actions never grow): short text, raw detail as a tooltip. */
export interface RowError {
  text: string;
  detail?: string;
}
export type OnRowError = (e: RowError | null) => void;

/** Tells the row about `text` while it is set, and takes it back when it changes or the action goes. */
export function useReportRowError(onError: OnRowError | undefined, text: string, detail?: string) {
  const report = useRef(onError);
  report.current = onError;
  useEffect(() => {
    if (!text) return;
    report.current?.({ text, detail: detail || undefined });
    return () => report.current?.(null);
  }, [text, detail]);
}

/** The errors of a list's rows, by row id; `set(id)` is stable per id-less call, so actions can depend on it. */
export function useRowErrors() {
  const [errors, setErrors] = useState<Record<string, RowError>>({});
  const set = useCallback((id: string, e: RowError | null) => {
    setErrors((m) => {
      const cur = m[id];
      if (cur?.text === e?.text && cur?.detail === e?.detail) return m;
      const next = { ...m };
      if (e) next[id] = e;
      else delete next[id];
      return next;
    });
  }, []);
  return [errors, set] as const;
}

/** The row's error line, under the title: muted red, one line, the raw detail in a tooltip. */
export function RowErrorLine({ error }: { error?: RowError }) {
  if (!error) return null;
  return (
    <span className="ellipsis small row-error" title={error.detail ?? error.text}>
      {error.text}
    </span>
  );
}
