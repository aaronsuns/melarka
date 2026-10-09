import { afterEach, expect, test, vi } from "vitest";
import { applyOfflineSwitch, type RegisterDeps } from "./register";
import { getOffline, setOfflineForTests } from "./index";
import { OfflineCache, type ManagerDeps } from "./manager";
import { FakeCaches } from "../test/offline";

function deps(over: Partial<RegisterDeps> = {}) {
  const reg = { update: vi.fn(async () => {}), unregister: vi.fn(async () => true) };
  const other = { update: vi.fn(async () => {}), unregister: vi.fn(async () => true) };
  const sw = {
    register: vi.fn(async () => reg),
    getRegistrations: vi.fn(async () => [reg, other]),
  };
  const names = ["lark-offline-v1", "lark-offline-v0", "something-else"];
  const caches = { keys: vi.fn(async () => names), delete: vi.fn(async () => true) };
  const d: RegisterDeps = {
    sw: sw as unknown as RegisterDeps["sw"],
    caches: caches as unknown as RegisterDeps["caches"],
    register: true,
    now: () => Date.now(),
    ...over,
  };
  return { d, reg, other, sw, caches };
}

afterEach(() => setOfflineForTests(undefined));

test("on: registers the worker at the root, and checks for an update when the app comes back to the front", async () => {
  const { d, sw, reg } = deps();
  const off = await applyOfflineSwitch(true, d);
  expect(sw.register).toHaveBeenCalledWith("/sw.js", { scope: "/" });
  Object.defineProperty(document, "visibilityState", { value: "visible", configurable: true });
  document.dispatchEvent(new Event("visibilitychange"));
  expect(reg.update).toHaveBeenCalledTimes(1);
  document.dispatchEvent(new Event("visibilitychange")); // throttled
  expect(reg.update).toHaveBeenCalledTimes(1);
  off();
});

test("on, in development: nothing is registered", async () => {
  const { d, sw } = deps({ register: false });
  await applyOfflineSwitch(true, d);
  expect(sw.register).not.toHaveBeenCalled();
});

test("off (the kill switch): never registers, unregisters every worker, deletes the offline caches, and turns the cache off", async () => {
  const m = new OfflineCache({
    caches: new FakeCaches() as unknown as ManagerDeps["caches"],
    fetch: async () => new Response(""),
    storage: localStorage,
    listFavorites: async () => [],
    online: () => true,
    busy: () => false,
    now: () => 0,
    notifyWorker: () => {},
    persist: async () => true,
    estimate: async () => null,
  });
  setOfflineForTests(m);
  const { d, sw, reg, other, caches } = deps();
  await applyOfflineSwitch(false, d);
  expect(sw.register).not.toHaveBeenCalled();
  expect(reg.unregister).toHaveBeenCalled();
  expect(other.unregister).toHaveBeenCalled();
  expect(caches.delete.mock.calls.map((c) => (c as unknown[])[0]).sort()).toEqual(["lark-offline-v0", "lark-offline-v1"]);
  expect(getOffline()).toBeNull();
});

test("without service workers or Cache Storage it does nothing and doesn't throw", async () => {
  await expect(applyOfflineSwitch(true, { sw: undefined, caches: undefined, register: true, now: () => 0 })).resolves.toBeTypeOf("function");
  await expect(applyOfflineSwitch(false, { sw: undefined, caches: undefined, register: true, now: () => 0 })).resolves.toBeTypeOf("function");
});
