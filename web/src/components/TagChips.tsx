import { Link } from "react-router";
import type { TagCount } from "../api/types";
import { has, t } from "../i18n/i18n";
import { useT } from "../i18n/i18n";

// Vocabulary slugs read through tag.<slug>; anything else is a free-form tag
// and shows as typed. Callers re-render on a locale change via useT().
export function tagLabel(name: string): string {
  const key = `tag.${name.toLowerCase()}`;
  return has(key) ? t(key) : name;
}

export function TagChips({ tags }: { tags: TagCount[] }) {
  useT();
  return (
    <div className="chips">
      {tags.map((tag) => (
        <Link key={tag.name} to={`/tags/${encodeURIComponent(tag.name)}`} className="chip tag-chip">
          {tagLabel(tag.name)} · {tag.count}
        </Link>
      ))}
    </div>
  );
}
