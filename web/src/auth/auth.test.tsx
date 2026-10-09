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

// Review focus 2: a session revoked elsewhere must drop back to login.
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
