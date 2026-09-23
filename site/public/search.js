/**
 * Client-side docs search over /search-index.json
 */
(() => {
  const input = document.querySelector("[data-docs-search]");
  const panel = document.querySelector("[data-docs-search-results]");
  if (!input || !panel) return;

  let index = null;
  let loadPromise = null;

  const load = () => {
    if (index) return Promise.resolve(index);
    if (loadPromise) return loadPromise;
    loadPromise = fetch("/search-index.json", { cache: "no-cache" })
      .then((r) => r.json())
      .then((data) => {
        index = data.pages || [];
        return index;
      })
      .catch(() => {
        index = [];
        return index;
      });
    return loadPromise;
  };

  const score = (page, q) => {
    const terms = q.toLowerCase().split(/\s+/).filter(Boolean);
    if (!terms.length) return 0;
    const hay = `${page.title} ${page.description} ${page.body}`.toLowerCase();
    let s = 0;
    for (const t of terms) {
      if (page.title.toLowerCase().includes(t)) s += 8;
      if (page.description.toLowerCase().includes(t)) s += 4;
      if (hay.includes(t)) s += 1;
      else return 0;
    }
    return s;
  };

  const render = (hits) => {
    if (!hits.length) {
      panel.hidden = false;
      const translate = window.oputeDocsI18n?.translate;
      const emptyText = translate ? translate("search.empty") : "No matches.";
      panel.innerHTML = `<p class="search-empty" data-i18n="search.empty">${escapeHtml(emptyText)}</p>`;
      return;
    }
    panel.hidden = false;
    panel.innerHTML = hits
      .slice(0, 8)
      .map(
        (h) =>
          `<a href="${h.url}"><strong>${escapeHtml(h.title)}</strong><span>${escapeHtml(h.description || "")}</span></a>`,
      )
      .join("");
  };

  const escapeHtml = (s) =>
    String(s)
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;");

  let timer = 0;
  input.addEventListener("input", () => {
    clearTimeout(timer);
    timer = setTimeout(async () => {
      const q = input.value.trim();
      if (q.length < 2) {
        panel.hidden = true;
        panel.innerHTML = "";
        return;
      }
      const pages = await load();
      const hits = pages
        .map((p) => ({ ...p, _s: score(p, q) }))
        .filter((p) => p._s > 0)
        .sort((a, b) => b._s - a._s);
      render(hits);
    }, 120);
  });

  input.addEventListener("keydown", (e) => {
    if (e.key === "Escape") {
      panel.hidden = true;
      input.blur();
    }
  });

  document.addEventListener("click", (e) => {
    if (!panel.contains(e.target) && e.target !== input) panel.hidden = true;
  });
})();
