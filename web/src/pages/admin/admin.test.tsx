import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Navigate, Route, Routes } from "react-router";
import AdminLayout from "./AdminLayout";
import UsersPage from "./UsersPage";
import SettingsPage from "../SettingsPage";
import ChangePassword from "../ChangePassword";
import { renderWithApp } from "../../test/render";

const routes = (
  <Routes>
    <Route path="/settings" element={<SettingsPage />} />
    <Route path="/change-password" element={<ChangePassword />} />
    <Route path="/admin" element={<AdminLayout />}>
      <Route index element={<Navigate to="users" replace />} />
      <Route path="users" element={<UsersPage />} />
    </Route>
  </Routes>
);

test("member sees no 管理 link on 我的", async () => {
  renderWithApp(routes, { role: "member", path: "/settings" });
  await screen.findByText("修改密码");
  expect(screen.queryByRole("link", { name: "管理" })).toBeNull();
});

test("admin sees the 管理 link on 我的", async () => {
  renderWithApp(routes, { role: "admin", path: "/settings" });
  expect(await screen.findByRole("link", { name: "管理" })).toHaveAttribute("href", "/admin");
});

test("/admin index redirects to the users tab", async () => {
  renderWithApp(routes, { role: "admin", path: "/admin", routes: { "GET /api/v1/users": () => ({ body: adminUsers }) } });
  await screen.findByText("bob");
  expect(screen.getByRole("button", { name: "新建用户" })).toBeInTheDocument();
});

test("a member hitting /admin/users is blocked without calling the users API", async () => {
  const { f } = renderWithApp(routes, { role: "member", path: "/admin/users" });
  expect(await screen.findByText("只有管理员可以访问")).toBeInTheDocument();
  expect(f.mock.calls.some((c) => String(c[0]).includes("/api/v1/users"))).toBe(false);
});

const adminUsers = [
  { id: 1, username: "u", role: "admin" },
  { id: 2, username: "bob", role: "member" },
];

test("admin creates a user", async () => {
  const post = vi.fn((init: RequestInit) => {
    const body = JSON.parse(init.body as string);
    return { status: 201, body: { id: 3, username: body.username, role: body.role } };
  });
  renderWithApp(routes, {
    role: "admin",
    path: "/admin/users",
    routes: { "GET /api/v1/users": () => ({ body: adminUsers }), "POST /api/v1/users": post },
  });
  await screen.findByText("bob");
  await userEvent.type(screen.getByPlaceholderText("用户名"), "carol");
  await userEvent.type(screen.getByPlaceholderText("密码"), "secret123");
  await userEvent.selectOptions(screen.getByLabelText("角色"), "admin");
  await userEvent.click(screen.getByRole("button", { name: "新建用户" }));
  await screen.findByText("carol");
  expect(JSON.parse(((post.mock.calls[0] as unknown[])[0] as RequestInit).body as string)).toEqual({
    username: "carol",
    password: "secret123",
    role: "admin",
  });
});

test("create shows the server's error for a taken username", async () => {
  renderWithApp(routes, {
    role: "admin",
    path: "/admin/users",
    routes: {
      "GET /api/v1/users": () => ({ body: adminUsers }),
      "POST /api/v1/users": () => ({ status: 409, body: { error: "username taken" } }),
    },
  });
  await screen.findByText("bob");
  await userEvent.type(screen.getByPlaceholderText("用户名"), "bob");
  await userEvent.type(screen.getByPlaceholderText("密码"), "secret123");
  await userEvent.click(screen.getByRole("button", { name: "新建用户" }));
  expect(await screen.findByText("username taken")).toBeInTheDocument();
});

test("admin resets a user's password", async () => {
  const put = vi.fn(() => ({ status: 204 }));
  renderWithApp(routes, {
    role: "admin",
    path: "/admin/users",
    routes: { "GET /api/v1/users": () => ({ body: adminUsers }), "PUT /api/v1/users/2/password": put },
  });
  await screen.findByText("bob");
  await userEvent.click(screen.getByRole("button", { name: "重置密码：bob" }));
  await userEvent.type(screen.getByLabelText("新密码：bob"), "newpass1");
  await userEvent.click(screen.getByRole("button", { name: "保存：bob" }));
  await vi.waitFor(() => expect(put).toHaveBeenCalled());
  expect(JSON.parse(((put.mock.calls[0] as unknown[])[0] as RequestInit).body as string)).toEqual({ password: "newpass1" });
  expect(screen.queryByLabelText("新密码：bob")).toBeNull();
});

test("delete needs two taps and is hidden for yourself", async () => {
  const del = vi.fn(() => ({ status: 204 }));
  renderWithApp(routes, {
    role: "admin",
    path: "/admin/users",
    routes: { "GET /api/v1/users": () => ({ body: adminUsers }), "DELETE /api/v1/users/2": del },
  });
  await screen.findByText("bob");
  expect(screen.queryByRole("button", { name: "删除：u" })).toBeNull();

  expect(del).not.toHaveBeenCalled();
  await userEvent.click(screen.getByRole("button", { name: "删除：bob" }));
  expect(screen.getByRole("button", { name: "确认删除：bob" })).toBeInTheDocument();
  expect(del).not.toHaveBeenCalled();
  await userEvent.click(screen.getByRole("button", { name: "确认删除：bob" }));
  await vi.waitFor(() => expect(del).toHaveBeenCalled());
  await vi.waitFor(() => expect(screen.queryByText("bob")).toBeNull());
});

test("delete confirm can be cancelled without deleting", async () => {
  const del = vi.fn(() => ({ status: 204 }));
  renderWithApp(routes, {
    role: "admin",
    path: "/admin/users",
    routes: { "GET /api/v1/users": () => ({ body: adminUsers }), "DELETE /api/v1/users/2": del },
  });
  await screen.findByText("bob");
  await userEvent.click(screen.getByRole("button", { name: "删除：bob" }));
  expect(screen.getByRole("button", { name: "确认删除：bob" })).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "取消删除：bob" }));
  expect(screen.getByRole("button", { name: "删除：bob" })).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "确认删除：bob" })).toBeNull();
  expect(del).not.toHaveBeenCalled();
});

test("change password mismatch shows the error without calling the API", async () => {
  const { f } = renderWithApp(routes, { role: "member", path: "/change-password" });
  await userEvent.type(screen.getByLabelText("当前密码"), "oldpass1");
  await userEvent.type(screen.getByLabelText("新密码"), "newpass1");
  await userEvent.type(screen.getByLabelText("重复新密码"), "newpass2");
  await userEvent.click(screen.getByRole("button", { name: "保存" }));
  expect(await screen.findByText("两次输入的新密码不一致")).toBeInTheDocument();
  expect(f.mock.calls.some((c) => String(c[0]).includes("/me/password"))).toBe(false);
});

test("change password success calls the API then logs out", async () => {
  const put = vi.fn(() => ({ status: 204 }));
  const logoutCall = vi.fn(() => ({ status: 401, body: { error: "not signed in" } }));
  const { f } = renderWithApp(routes, {
    role: "member",
    path: "/change-password",
    routes: { "PUT /api/v1/me/password": put, "POST /api/v1/auth/logout": logoutCall },
  });
  await userEvent.type(screen.getByLabelText("当前密码"), "oldpass1");
  await userEvent.type(screen.getByLabelText("新密码"), "newpass1");
  await userEvent.type(screen.getByLabelText("重复新密码"), "newpass1");
  await userEvent.click(screen.getByRole("button", { name: "保存" }));
  expect(await screen.findByText("密码已修改，请重新登录")).toBeInTheDocument();
  expect(JSON.parse(((put.mock.calls[0] as unknown[])[0] as RequestInit).body as string)).toEqual({ current: "oldpass1", new: "newpass1" });
  await vi.waitFor(() => expect(logoutCall).toHaveBeenCalled());
  expect(f.mock.calls.findIndex((c) => String(c[0]).includes("/me/password"))).toBeLessThan(
    f.mock.calls.findIndex((c) => String(c[0]).includes("/auth/logout")),
  );
});
