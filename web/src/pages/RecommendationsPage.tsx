import { RecommendationList } from "../components/RecommendationList";
import { useRecommendations } from "../components/useRecommendations";
import { useT } from "../i18n/i18n";

/** All of the user's recommendations, with 刷新. */
export default function RecommendationsPage() {
  const t = useT();
  const { data, loadError, refreshing, refresh, refreshError, setItems } = useRecommendations();
  return (
    <>
      <h1 className="page-title">{t("recs.title")}</h1>
      {data?.enabled !== false && (
        <button className="secondary compact" onClick={refresh} disabled={refreshing}>
          {refreshing ? t("recs.refreshing") : t("recs.refresh")}
        </button>
      )}
      {refreshError && <p className="error small">{refreshError}</p>}
      {loadError && <p className="error small">{t("recs.loadFailed")}</p>}
      {data && !data.enabled && <p className="muted">{t("recs.off")}</p>}
      {data?.enabled && data.items.length === 0 && <p className="muted">{t("recs.empty")}</p>}
      {data?.enabled && data.items.length > 0 && <RecommendationList items={data.items} onChange={setItems} />}
    </>
  );
}
