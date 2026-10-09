import { useT } from "../i18n/i18n";

export function More({ loading, done, error, loadMore, sentinel }: { loading: boolean; done: boolean; error: string; loadMore(): void; sentinel: (el: Element | null) => void }) {
  const t = useT();
  if (error) return <p className="error">{error}</p>;
  if (done) return null;
  return (
    <div ref={sentinel} className="more">
      <button onClick={loadMore} disabled={loading}>{loading ? t("common.loading") : t("common.loadMore")}</button>
    </div>
  );
}
