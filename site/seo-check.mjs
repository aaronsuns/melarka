#!/usr/bin/env node
// SEO lint + sitemap for a static site. Zero dependencies (Node >= 18).
// Vendored into each product repo's site/ by sync.sh; the source lives in aaron/sites/seo-kit.
//   node seo-check.mjs [publicDir] [--origin https://x.zexa.se] [--write-sitemap]
// Exit 1 on any error; warnings never fail.
import { readdirSync, readFileSync, writeFileSync, statSync, existsSync } from "node:fs";
import { join, relative, dirname, resolve } from "node:path";

const args = process.argv.slice(2);
const flag = (n) => args.includes(n);
const opt = (n) => { const i = args.indexOf(n); return i >= 0 ? args[i + 1] : null; };
const dir = resolve(args.find((a, i) => !a.startsWith("--") && args[i - 1] !== "--origin") || "public");

const walk = (d) => readdirSync(d).flatMap((f) => {
  const p = join(d, f);
  return statSync(p).isDirectory() ? walk(p) : [p];
});
const files = walk(dir);
const pages = files.filter((f) => f.endsWith(".html") && !/\/(thanks|404)\.html$/.test(f));
const urlPath = (f) => {
  let u = "/" + relative(dir, f).replace(/\\/g, "/");
  u = u.replace(/index\.html$/, "").replace(/\.html$/, "");
  return u === "" ? "/" : u;
};

let origin = opt("--origin");
if (!origin) {
  const home = pages.find((f) => urlPath(f) === "/");
  const m = home && /<link rel="canonical" href="(https?:\/\/[^/"]+)/.exec(readFileSync(home, "utf8"));
  origin = m ? m[1] : null;
}

const errors = [], warns = [];
const E = (f, m) => errors.push(`${relative(dir, f)}: ${m}`);
const W = (f, m) => warns.push(`${relative(dir, f)}: ${m}`);
const seenTitles = new Map(), seenDescs = new Map();

for (const f of pages) {
  const h = readFileSync(f, "utf8");
  const path = urlPath(f);
  const get = (re) => (re.exec(h) || [])[1];
  const title = get(/<title>([^<]*)<\/title>/);
  const desc = get(/<meta name="description" content="([^"]*)"/);
  const canon = get(/<link rel="canonical" href="([^"]*)"/);
  if (!/<html[^>]*\blang="/.test(h)) E(f, "missing <html lang>");
  if (!title) E(f, "missing <title>");
  else {
    if (title.length < 25 || title.length > 70) W(f, `title length ${title.length} (aim 30-65)`);
    if (seenTitles.has(title)) E(f, `duplicate title, also ${seenTitles.get(title)}`);
    seenTitles.set(title, relative(dir, f));
  }
  if (!desc) E(f, "missing meta description");
  else {
    if (desc.length < 70 || desc.length > 165) W(f, `description length ${desc.length} (aim 90-160)`);
    if (seenDescs.has(desc)) E(f, `duplicate description, also ${seenDescs.get(desc)}`);
    seenDescs.set(desc, relative(dir, f));
  }
  if (!canon) E(f, "missing canonical");
  else if (origin && canon !== origin + (path === "/" ? "/" : path)) E(f, `canonical ${canon} != ${origin}${path}`);
  for (const p of ["og:title", "og:description", "og:url", "og:image", "og:type"]) {
    if (!new RegExp(`property="${p}"`).test(h)) E(f, `missing ${p}`);
  }
  if (!/name="twitter:card"/.test(h)) W(f, "missing twitter:card");
  const h1s = (h.match(/<h1[\s>]/g) || []).length;
  if (h1s === 0) E(f, "no <h1>");
  const ld = [...h.matchAll(/<script type="application\/ld\+json">([\s\S]*?)<\/script>/g)];
  if (!ld.length) W(f, "no JSON-LD");
  for (const m of ld) { try { JSON.parse(m[1]); } catch (e) { E(f, `JSON-LD does not parse: ${e.message}`); } }
  for (const m of h.matchAll(/<img\b[^>]*>/g)) if (!/\balt=/.test(m[0])) W(f, `img without alt: ${m[0].slice(0, 60)}`);
  const words = h.replace(/<(script|style)[\s\S]*?<\/\1>/g, "").replace(/<[^>]+>/g, " ").split(/\s+/).filter(Boolean).length;
  if (words < 250 && !/data-l=/.test(h)) W(f, `thin page: ${words} words`);
  for (const m of h.matchAll(/<a\b[^>]*\bhref="(\/[^"#?]*)/g)) {
    const t = m[1];
    if (t === "/") continue;
    const cand = [join(dir, t), join(dir, t + ".html"), join(dir, t, "index.html")];
    if (!cand.some((c) => existsSync(c) && statSync(c).isFile())) W(f, `broken internal link ${t}`);
  }
}

if (flag("--write-sitemap")) {
  if (!origin) { console.error("need --origin or a canonical on /"); process.exit(1); }
  const today = new Date().toISOString().slice(0, 10);
  const rows = pages.map((f) => `  <url><loc>${origin}${urlPath(f)}</loc><lastmod>${today}</lastmod></url>`).sort();
  writeFileSync(join(dir, "sitemap.xml"), `<?xml version="1.0" encoding="UTF-8"?>\n<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">\n${rows.join("\n")}\n</urlset>\n`);
  console.log(`sitemap.xml: ${rows.length} url(s)`);
}

const sm = existsSync(join(dir, "sitemap.xml")) ? readFileSync(join(dir, "sitemap.xml"), "utf8") : "";
if (!sm) E(join(dir, "sitemap.xml"), "missing sitemap.xml");
else for (const f of pages) if (origin && !sm.includes(`<loc>${origin}${urlPath(f)}</loc>`)) E(f, "not in sitemap.xml");
const rb = existsSync(join(dir, "robots.txt")) ? readFileSync(join(dir, "robots.txt"), "utf8") : "";
if (!/Sitemap:/i.test(rb)) E(join(dir, "robots.txt"), "robots.txt missing or has no Sitemap: line");

console.log(`${pages.length} page(s) checked in ${dir}`);
for (const w of warns) console.log("warn  " + w);
for (const e of errors) console.log("ERROR " + e);
process.exit(errors.length ? 1 : 0);
