import { StrictMode } from "react";
import { act, renderHook, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { useInfinite } from "./useInfinite";

describe("useInfinite", () => {
  it("a stale loadMore called after the last page does not refetch page 1 (duplicate rows)", async () => {
    const fetchPage = vi.fn(async (_cursor: string) => ({ items: [{ id: 1 }], next_cursor: "" }));
    const { result } = renderHook(() => useInfinite(fetchPage, []));
    // What an IntersectionObserver callback registered before the list finished holds.
    const staleLoadMore = result.current.loadMore;
    await waitFor(() => expect(result.current.done).toBe(true));
    act(() => staleLoadMore());
    await act(async () => {});
    expect(fetchPage).toHaveBeenCalledTimes(1);
    expect(result.current.items).toEqual([{ id: 1 }]);
  });
});

describe("useInfinite under StrictMode", () => {
  it("applies each page once even though mount effects run twice", async () => {
    const fetchPage = vi.fn(async (_cursor: string) => ({ items: [{ id: 1 }], next_cursor: "" }));
    const { result } = renderHook(() => useInfinite(fetchPage, []), { wrapper: StrictMode });
    await waitFor(() => expect(result.current.done).toBe(true));
    await act(async () => {});
    expect(result.current.items).toEqual([{ id: 1 }]);
  });
});
