// Counts a page load that arrived with ?ref=<channel> (our promo-link tag), once per browser session: GET dash.zexa.se/b. No cookies, no IP stored.
(() => { try {
  const r = new URLSearchParams(location.search).get("ref");
  if (!r || sessionStorage.getItem("rb")) return;
  sessionStorage.setItem("rb", "1");
  fetch("https://dash.zexa.se/b?h=" + encodeURIComponent(location.hostname) + "&r=" + encodeURIComponent(r), { mode: "no-cors", keepalive: true });
} catch (e) {} })();
