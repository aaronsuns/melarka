import { NavLink, Outlet, useMatch } from "react-router";
import { useAuth } from "../../auth/AuthProvider";
import { useT } from "../../i18n/i18n";

function links(t: ReturnType<typeof useT>) {
  return [
    ["users", t("admin.nav.users")],
    ["pending", t("admin.nav.pending")],
    ["downloads", t("admin.nav.downloads")],
    ["trash", t("admin.nav.trash")],
    ["libraries", t("admin.nav.libraries")],
    ["system", t("admin.nav.system")],
  ] as const;
}

function Tab({ to, label }: { to: string; label: string }) {
  // Visual selection only: react-router's own active-link matching, not
  // used for anything load-bearing.
  const active = !!useMatch(`/admin/${to}`);
  return (
    <NavLink to={to} role="tab" aria-selected={active}>
      {label}
    </NavLink>
  );
}

export default function AdminLayout() {
  const { user } = useAuth();
  const t = useT();
  if (user?.role !== "admin") return <p className="error">{t("admin.nav.forbidden")}</p>;
  return (
    <>
      <h1 className="page-title">{t("admin.nav.title")}</h1>
      <div className="segmented" role="tablist">
        {links(t).map(([to, label]) => (
          <Tab key={to} to={to} label={label} />
        ))}
      </div>
      <Outlet />
    </>
  );
}
