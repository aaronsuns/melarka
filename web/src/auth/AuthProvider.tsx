import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { api, setUnauthorizedHandler } from "../api/client";
import type { User } from "../api/types";
import { hasNative, nativePost, onNative } from "../native/bridge";

interface AuthState {
  user: User | null;
  loading: boolean;
  login(username: string, password: string): Promise<void>;
  logout(): Promise<void>;
  /** Signs in to one more account on this device and switches to it (Settings → Accounts). */
  addAccount(username: string, password: string): Promise<void>;
  /** Switches to another account signed in on this device, without a password. */
  switchAccount(userId: number): Promise<void>;
}

const Ctx = createContext<AuthState | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<User | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    setUnauthorizedHandler(() => setUser(null));
    api.me().then(setUser).catch(() => setUser(null)).finally(() => setLoading(false));
  }, []);

  // Inside the iPhone app, native follows the web's session: it takes the
  // token from the session cookie when told of a sign-in, and stops, wipes
  // the queues and forgets the token on a sign-out. Only a real sign-out is
  // one: a page that opens signed out (or whose /me failed, e.g. offline)
  // says nothing, or a flaky start would wipe native's queue. Native's own
  // 401 (authRequired) signs the web out too.
  const signedIn = useRef(false);
  useEffect(() => {
    if (!hasNative() || loading) return;
    if (user) {
      signedIn.current = true;
      nativePost({ type: "auth", signedIn: true, userId: user.id });
    } else if (signedIn.current) {
      signedIn.current = false;
      nativePost({ type: "auth", signedIn: false });
    }
  }, [user, loading]);
  useEffect(
    () =>
      hasNative()
        ? onNative((m) => {
            if (m.type !== "authRequired") return;
            signedIn.current = false; // native signed itself out: nothing to tell it back
            setUser(null);
          })
        : undefined,
    [],
  );

  const login = useCallback(async (u: string, p: string) => {
    setUser(await api.login(u, p));
  }, []);
  const logout = useCallback(async () => {
    await api.logout().catch(() => {});
    setUser(null);
  }, []);

  // A new user remounts the signed-in app (Gate keys it by user id), and in the native apps the effect
  // above posts auth with the new userId, which switches native's user like any sign-in.
  const addAccount = useCallback(async (u: string, p: string) => {
    setUser(await api.addAccount(u, p));
  }, []);
  const switchAccount = useCallback(async (id: number) => {
    setUser(await api.switchAccount(id));
  }, []);

  const value = useMemo(
    () => ({ user, loading, login, logout, addAccount, switchAccount }),
    [user, loading, login, logout, addAccount, switchAccount],
  );
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useAuth(): AuthState {
  const v = useContext(Ctx);
  if (!v) throw new Error("useAuth outside AuthProvider");
  return v;
}
