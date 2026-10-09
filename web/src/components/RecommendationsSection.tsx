import { Link } from "react-router";
import { useT } from "../i18n/i18n";
import { RecommendationList } from "./RecommendationList";
import { useRecommendations } from "./useRecommendations";

const HOME_ROWS = 6;

/** Home's "为你推荐": the first 6 rows and 查看全部; nothing when switched off or unreachable. */
export function RecommendationsSection() {
  const t = useT();
  const { data, setItems } = useRecommendations();
  if (!data?.enabled) return null;
  return (
    <section className="recs-section">
      <div className="section-head">
        <h2 className="section-title">{t("recs.title")}</h2>
        {/* Always there: a new user's empty list is refreshed from the page. */}
        <Link to="/recommendations" className="small">{t("recs.seeAll")}</Link>
      </div>
      {data.items.length === 0 ? (
        <p className="muted small">{t("recs.empty")}</p>
      ) : (
        <RecommendationList items={data.items.slice(0, HOME_ROWS)} onChange={setItems} />
      )}
    </section>
  );
}
