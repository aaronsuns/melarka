import { Link } from "react-router";
import { useAuth } from "../auth/AuthProvider";
import { useT } from "../i18n/i18n";

// First run with no songs: say how to point Melarka at a music folder (the admin) or who to ask (everyone else).
export function EmptyLibrary() {
  const t = useT();
  const { user } = useAuth();
  return (
    <section className="notice-strip empty-library" data-testid="empty-library">
      <div>
        <h2 className="section-title">{t("empty.title")}</h2>
        {user?.role === "admin" ? (
          <>
            <ol className="small">
              <li>{t("empty.step1")}</li>
              <li>{t("empty.step2")}</li>
              <li>{t("empty.step3")}</li>
            </ol>
            <Link to="/admin/libraries" className="chip">{t("empty.openLibraries")}</Link>
          </>
        ) : (
          <p className="small">{t("empty.member")}</p>
        )}
      </div>
    </section>
  );
}
