/**
 * Client-side docs search with distinct loading, failure, retry, and zero-result states.
 */
(() => {
  const input = document.querySelector("[data-docs-search]");
  const panel = document.querySelector("[data-docs-search-results]");
  if (!input || !panel) return;

  let index = null;
  let loadPromise = null;
  let timer = 0;

  const setPanelLanguage = () => {
    panel.lang = document.documentElement.lang === "fr" ? "fr" : "en";
  };

  const message = (key, fallback) => {
    const translate = window.oputeDocsI18n?.translate;
    return translate ? translate(key) : fallback;
  };

  const showStatus = (className, text) => {
    setPanelLanguage();
    const status = document.createElement("p");
    status.className = className;
    status.setAttribute("role", "status");
    status.textContent = text;
    panel.replaceChildren(status);
    panel.hidden = false;
  };

  const escapeText = (value) => String(value ?? "");
  const score = (page, query) => {
    const title = escapeText(page.title);
    const description = escapeText(page.description);
    const body = escapeText(page.body);
    const terms = query.toLowerCase().split(/\s+/).filter(Boolean);
    const haystack = (title + " " + description + " " + body).toLowerCase();
    let total = 0;
    for (const term of terms) {
      if (title.toLowerCase().includes(term)) total += 8;
      if (description.toLowerCase().includes(term)) total += 4;
      if (haystack.includes(term)) total += 1;
      else return 0;
    }
    return total;
  };

  const load = () => {
    if (index) return Promise.resolve(index);
    if (loadPromise) return loadPromise;
    loadPromise = fetch("/search-index.json", { cache: "no-cache" })
      .then((response) => {
        if (!response.ok) throw new Error("search index request failed");
        return response.json();
      })
      .then((data) => {
        if (!data || !Array.isArray(data.pages)) throw new Error("search index is malformed");
        const pages = data.pages;
        if (
          !pages.length ||
          pages.some(
            (page) =>
              !page ||
              typeof page.url !== "string" ||
              typeof page.title !== "string" ||
              typeof page.description !== "string" ||
              typeof page.body !== "string",
          )
        ) {
          throw new Error("search index has malformed pages");
        }
        index = pages;
        return index;
      })
      .catch((error) => {
        loadPromise = null;
        throw error;
      });
    return loadPromise;
  };

  const render = (hits) => {
    if (!hits.length) {
      showStatus("search-empty", message("search.empty", "No matches."));
      return;
    }
    setPanelLanguage();
    const links = hits.slice(0, 8).map((hit) => {
      const link = document.createElement("a");
      link.href = hit.url;
      link.lang = "en";
      const title = document.createElement("strong");
      title.textContent = hit.title;
      const summary = document.createElement("span");
      summary.textContent = hit.description || "";
      link.append(title, summary);
      return link;
    });
    panel.replaceChildren(...links);
    panel.hidden = false;
  };

  const showFailure = () => {
    setPanelLanguage();
    const status = document.createElement("p");
    status.className = "search-error";
    status.setAttribute("role", "status");
    status.textContent = message("search.error", "Could not load documentation search.");
    const retry = document.createElement("button");
    retry.type = "button";
    retry.className = "search-retry";
    retry.textContent = message("search.retry", "Retry search");
    retry.addEventListener("click", () => {
      void runSearch();
    });
    panel.replaceChildren(status, retry);
    panel.hidden = false;
  };

  async function runSearch() {
    const query = input.value.trim();
    if (query.length < 2) {
      panel.hidden = true;
      panel.replaceChildren();
      return;
    }
    showStatus("search-loading", message("search.loading", "Loading search index."));
    try {
      const pages = await load();
      if (input.value.trim() !== query) return;
      const hits = pages
        .map((page) => ({ ...page, _score: score(page, query) }))
        .filter((page) => page._score > 0)
        .sort((left, right) => right._score - left._score);
      render(hits);
    } catch {
      if (input.value.trim() === query) showFailure();
    }
  }

  input.addEventListener("input", () => {
    clearTimeout(timer);
    timer = setTimeout(() => void runSearch(), 120);
  });

  input.addEventListener("keydown", (event) => {
    if (event.key === "Escape") {
      panel.hidden = true;
      input.blur();
    }
  });

  document.addEventListener("click", (event) => {
    if (!panel.contains(event.target) && event.target !== input) panel.hidden = true;
  });
})();
