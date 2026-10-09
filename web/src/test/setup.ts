import "@testing-library/jest-dom/vitest";
import { vi, afterEach } from "vitest";
import { forgetFailedCovers } from "../components/Cover";
import { setLocale, setServerDefault, setUserLanguage } from "../i18n/i18n";

// The whole suite runs in zh-Hans (the e2e default and the locale most of
// the existing Chinese-string assertions were written against); any test
// that calls setLocale/setServerDefault/setUserLanguage itself must leave
// it the way it found it, which afterEach below enforces regardless.
setLocale("zh-Hans");

type Handler = (init: RequestInit, url: string) => { status?: number; body?: unknown; headers?: Record<string, string> };

// mockFetch keys are "METHOD /path" with the query string stripped; the
// handler gets the RequestInit and the full URL.
export function mockFetch(routes: Record<string, Handler>) {
  const fn = vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
    const url = typeof input === "string" ? input : input.toString();
    const path = url.split("?")[0];
    const key = `${(init.method ?? "GET").toUpperCase()} ${path}`;
    const h = routes[key];
    if (!h) return new Response(JSON.stringify({ error: `no mock for ${key}` }), { status: 599 });
    const { status = 200, body, headers } = h(init, url);
    return new Response(body === undefined ? null : JSON.stringify(body), { status, headers: { "Content-Type": "application/json", ...headers } });
  });
  vi.stubGlobal("fetch", fn);
  return fn;
}

afterEach(() => {
  vi.unstubAllGlobals();
  localStorage.clear();
  setUserLanguage(null);
  setServerDefault(null);
  setLocale("zh-Hans");
  forgetFailedCovers();
});

// jsdom doesn't implement real media playback. A few tests exercise a
// genuine <audio> element (PlayerProvider without an injected FakeAudio, as
// in the full App via auth.test.tsx's login/logout flow), and calling
// pause()/load() on it otherwise spams "not implemented" to stderr. Stub
// them so the suite's output stays clean.
if (typeof HTMLMediaElement !== "undefined") {
  HTMLMediaElement.prototype.play = vi.fn(() => Promise.resolve());
  HTMLMediaElement.prototype.pause = vi.fn();
  HTMLMediaElement.prototype.load = vi.fn();
}

export class FakeAudio extends EventTarget {
  private _src = "";
  // Exposed so tests can tell whether a `src` (re)assignment happened after
  // an error — real <audio> elements clear their error state and reset
  // currentTime to 0 whenever `src` is (re)assigned, even to the same
  // value, because it restarts the resource-selection algorithm.
  errored = false;
  // Tests set this before fire("error") to pick a MediaError code
  // (2 = network, 3 = decode, 4 = not supported); cleared on src change.
  error: { code: number } | null = null;
  muted = false;
  volume = 1;
  playbackRate = 1;
  defaultPlaybackRate = 1;
  currentTime = 0;
  duration = NaN;
  paused = true;
  seeking = false;
  // HAVE_ENOUGH_DATA / NETWORK_IDLE unless a test says otherwise.
  readyState = 4;
  networkState = 1;
  get src() {
    return this._src;
  }
  set src(v: string) {
    this._src = v;
    this.errored = false;
    this.error = null;
    this.currentTime = 0;
  }
  play = vi.fn(() => {
    if (this.errored) return Promise.reject(new Error("no supported source"));
    this.paused = false;
    this.fire("play");
    return Promise.resolve();
  });
  pause = vi.fn(() => {
    this.paused = true;
    this.fire("pause");
  });
  load = vi.fn();
  removeAttribute = vi.fn((n: string) => {
    if (n === "src") this._src = "";
  });
  fire(type: string) {
    if (type === "error") this.errored = true;
    this.dispatchEvent(new Event(type));
  }
}
