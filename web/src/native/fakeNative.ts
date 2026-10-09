// Tests only: a fake `window.larkNative` (the Lark iPhone app's bridge).
import { act } from "@testing-library/react";
import { vi, type Mock } from "vitest";
import { NATIVE_EVENT, type FromNative, type ItemKind, type NativeState, type ToNative } from "./bridge";

export interface FakeNative {
  post: Mock<(m: ToNative) => void>;
  /** Native → web: dispatches a `lark-native` CustomEvent inside act(). */
  emit(m: FromNative): void;
  /** Every message posted so far, of one type. */
  sent<T extends ToNative["type"]>(type: T): Extract<ToNative, { type: T }>[];
  uninstall(): void;
}

export function installFakeNative(): FakeNative {
  const post = vi.fn<(m: ToNative) => void>();
  Object.defineProperty(window, "larkNative", { value: { version: 1, post }, configurable: true, writable: true });
  return {
    post,
    emit(m) {
      act(() => {
        window.dispatchEvent(new CustomEvent(NATIVE_EVENT, { detail: m }));
      });
    },
    sent(type) {
      return post.mock.calls.map(([m]) => m).filter((m): m is never => m.type === type);
    },
    uninstall() {
      delete (window as { larkNative?: unknown }).larkNative;
    },
  };
}

export function stateEvent(p: Partial<NativeState> & { kind: ItemKind }): NativeState {
  return { type: "state", itemId: null, index: 0, playing: false, positionMs: 0, durationMs: 0, buffering: false, error: null, rate: 1, ...p };
}
