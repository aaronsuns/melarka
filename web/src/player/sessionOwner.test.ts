import { afterEach, expect, test, vi } from "vitest";
import { claimSession, onSessionClaim, ownsSession, resetSessionOwner } from "./sessionOwner";

afterEach(() => resetSessionOwner());

test("music owns the session until someone claims it; listeners hear changes only", () => {
  expect(ownsSession("music")).toBe(true);
  const seen = vi.fn();
  const off = onSessionClaim(seen);
  claimSession("music");
  expect(seen).not.toHaveBeenCalled();
  claimSession("episode");
  expect(ownsSession("episode")).toBe(true);
  expect(ownsSession("music")).toBe(false);
  expect(seen).toHaveBeenCalledWith("episode");
  off();
  claimSession("music");
  expect(seen).toHaveBeenCalledTimes(1);
});

test("a forced claim tells the listeners even when that player already owns the session", () => {
  const seen = vi.fn();
  const off = onSessionClaim(seen);
  claimSession("music", { force: true });
  expect(seen).toHaveBeenCalledWith("music");
  off();
});
