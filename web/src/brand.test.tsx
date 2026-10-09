import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import App from "./App";
import LoginPage from "./auth/LoginPage";
import { AuthProvider } from "./auth/AuthProvider";
import { mockFetch } from "./test/setup";

// The product is Melarka (the code name lark stays in identifiers only).
test("the splash says Melarka, never Lark", () => {
  mockFetch({ "GET /api/v1/me": () => ({ status: 401, body: { error: "x" } }) });
  render(<MemoryRouter><App /></MemoryRouter>);
  expect(screen.getByText("Melarka")).toBeInTheDocument();
  expect(screen.queryByText("Lark")).toBeNull();
});

test("the login page says Melarka, never Lark", async () => {
  mockFetch({ "GET /api/v1/me": () => ({ status: 401, body: { error: "x" } }) });
  render(<MemoryRouter><AuthProvider><LoginPage /></AuthProvider></MemoryRouter>);
  expect(await screen.findByText("Melarka")).toBeInTheDocument();
  expect(screen.queryByText("Lark")).toBeNull();
});
