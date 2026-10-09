import { useCallback, useEffect, useRef, useState, type Dispatch, type RefCallback, type SetStateAction } from "react";
import type { Page } from "../api/types";
import { errorMessage } from "../i18n/errors";

export function useInfinite<T>(fetchPage: (cursor: string) => Promise<Page<T>>, deps: unknown[]) {
  const [items, setItems] = useState<T[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState(false);
  const cursor = useRef("");
  const busy = useRef(false);
  // Mirrors `done` for callers holding a stale loadMore (IntersectionObserver
  // callbacks, queued clicks): an exhausted list has cursor "", which the
  // server reads as "first page", so one more call would append page 1 again.
  const finished = useRef(false);
  const gen = useRef(0);
  const fetchRef = useRef(fetchPage);
  fetchRef.current = fetchPage;

  const loadMore = useCallback(() => {
    if (busy.current || done || finished.current) return;
    busy.current = true;
    setLoading(true);
    const g = gen.current;
    fetchRef.current(cursor.current)
      .then((p) => {
        if (g !== gen.current) return;
        setItems((prev) => [...prev, ...p.items]);
        cursor.current = p.next_cursor;
        if (!p.next_cursor) {
          finished.current = true;
          setDone(true);
        }
      })
      .catch((e) => g === gen.current && setError(errorMessage(e, "common.loadFailed")))
      .finally(() => {
        if (g === gen.current) {
          busy.current = false;
          setLoading(false);
        }
      });
  }, [done]);

  useEffect(() => {
    gen.current += 1;
    cursor.current = "";
    busy.current = false;
    finished.current = false;
    setItems([]);
    setDone(false);
    setError("");
  }, deps);

  useEffect(() => {
    if (items.length === 0 && !done && !busy.current && !error) loadMore();
  }, [items.length, done, error, loadMore]);

  const observer = useRef<IntersectionObserver | null>(null);
  const sentinel: RefCallback<Element> = useCallback(
    (el) => {
      observer.current?.disconnect();
      if (!el || typeof IntersectionObserver === "undefined") return;
      observer.current = new IntersectionObserver((es) => es.some((e) => e.isIntersecting) && loadMore(), { rootMargin: "400px" });
      observer.current.observe(el);
    },
    [loadMore],
  );

  return { items, setItems: setItems as Dispatch<SetStateAction<T[]>>, loading, error, done, loadMore, sentinel };
}
