import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import App from "../App";
import { mockFetch } from "../test/setup";

const renderApp = (path = "/") => render(<MemoryRouter initialEntries={[path]}><App /></MemoryRouter>);

test("shows the login page when there is no session", async () => {
  mockFetch({ "GET /api/v1/me": () => ({ status: 401, body: { error: "not signed in" } }) });
  renderApp();
  expect(await screen.findByRole("button", { name: "登录" })).toBeInTheDocument();
});

test("logs in and shows the shell", async () => {
  let signedIn = false;
  mockFetch({
    "GET /api/v1/me": () => (signedIn ? { body: { id: 1, username: "alice", role: "admin" } } : { status: 401, body: { error: "x" } }),
    "POST /api/v1/auth/login": () => { signedIn = true; return { body: { token: "t", user: { id: 1, username: "alice", role: "admin" } } }; },
  });
  renderApp();
  await userEvent.type(await screen.findByLabelText("用户名"), "alice");
  await userEvent.type(screen.getByLabelText("密码"), "pw");
  await userEvent.click(screen.getByRole("button", { name: "登录" }));
  expect(await screen.findByRole("link", { name: "音乐库" })).toBeInTheDocument();
});

test("shows the server's error on a bad password", async () => {
  mockFetch({
    "GET /api/v1/me": () => ({ status: 401, body: { error: "x" } }),
    "POST /api/v1/auth/login": () => ({ status: 401, body: { error: "wrong username or password" } }),
  });
  renderApp();
  await userEvent.type(await screen.findByLabelText("用户名"), "a");
  await userEvent.type(screen.getByLabelText("密码"), "b");
  await userEvent.click(screen.getByRole("button", { name: "登录" }));
  expect(await screen.findByText("wrong username or password")).toBeInTheDocument();
});

// A session revoked elsewhere must drop back to login.
test("a 401 from any API call returns to the login page", async () => {
  let revoked = false;
  mockFetch({
    "GET /api/v1/me": () => (revoked ? { status: 401, body: { error: "x" } } : { body: { id: 1, username: "alice", role: "admin" } }),
    "GET /api/v1/playlists": () => { revoked = true; return { status: 401, body: { error: "not signed in" } }; },
  });
  const { api } = await import("../api/client");
  renderApp();
  await screen.findByRole("link", { name: "音乐库" });
  await api.playlists().catch(() => {});
  await waitFor(() => expect(screen.getByRole("button", { name: "登录" })).toBeInTheDocument());
});

// Quick account switch: 我的 → accounts on this device.
test("switches to another account on this device with one tap", async () => {
  let switched: unknown = null;
  mockFetch({
    "GET /api/v1/me": () => ({ body: { id: 1, username: "dad", role: "admin" } }),
    "GET /api/v1/auth/accounts": () => ({ body: [
      { user_id: 1, username: "dad", current: true },
      { user_id: 2, username: "kid", current: false },
    ] }),
    "POST /api/v1/auth/switch": (init) => {
      switched = JSON.parse(init.body as string);
      return { body: { token: "t2", user: { id: 2, username: "kid", role: "member" } } };
    },
  });
  renderApp("/settings");
  await userEvent.click(await screen.findByRole("button", { name: "切换到 kid" }));
  expect(await screen.findByText(/kid/, { selector: "p" })).toBeInTheDocument();
  expect(switched).toEqual({ user_id: 2 });
});

test("adds an account with its password once and switches to it", async () => {
  let sent: Record<string, unknown> | null = null;
  mockFetch({
    "GET /api/v1/me": () => ({ body: { id: 1, username: "dad", role: "admin" } }),
    "GET /api/v1/auth/accounts": () => ({ body: [{ user_id: 1, username: "dad", current: true }] }),
    "POST /api/v1/auth/login": (init) => {
      sent = JSON.parse(init.body as string);
      return { body: { token: "t2", user: { id: 2, username: "kid", role: "member" } } };
    },
  });
  renderApp("/settings");
  await userEvent.click(await screen.findByRole("button", { name: "添加账号" }));
  await userEvent.type(screen.getByLabelText("用户名"), "kid");
  await userEvent.type(screen.getByLabelText("密码"), "pw");
  await userEvent.click(screen.getByRole("button", { name: "登录并切换" }));
  expect(await screen.findByText(/kid/, { selector: "p" })).toBeInTheDocument();
  expect(sent).toMatchObject({ username: "kid", password: "pw", link: true });
});
