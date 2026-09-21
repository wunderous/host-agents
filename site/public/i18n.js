/**
 * UI locale switcher. Prose pages stay English; chrome strings translate.
 * Persists choice in localStorage (`opute-docs-lang`).
 */
(() => {
  const STRINGS = {
    en: {
      "nav.docs": "Docs",
      "nav.getStarted": "Get started",
      "nav.search": "Search docs",
      "search.placeholder": "Search docs…",
      "search.empty": "No matches.",
      "lang.label": "Language",
      "footer.facts": "Facts track the repository",
      "footer.openapi": "OpenAPI",
      "i18n.banner": "",
    },
    es: {
      "nav.docs": "Docs",
      "nav.getStarted": "Empezar",
      "nav.search": "Buscar docs",
      "search.placeholder": "Buscar en la documentación…",
      "search.empty": "Sin resultados.",
      "lang.label": "Idioma",
      "footer.facts": "Los hechos siguen el repositorio",
      "footer.openapi": "OpenAPI",
      "i18n.banner":
        "La interfaz está en español; el contenido de las páginas permanece en inglés por ahora.",
    },
  };

  const supported = Object.keys(STRINGS);
  const params = new URLSearchParams(location.search);
  const stored = localStorage.getItem("opute-docs-lang");
  const initial = params.get("lang") || stored || document.documentElement.lang || "en";
  const lang = supported.includes(initial) ? initial : "en";

  const apply = (next) => {
    const dict = STRINGS[next] || STRINGS.en;
    document.documentElement.lang = next;
    localStorage.setItem("opute-docs-lang", next);
    document.querySelectorAll("[data-i18n]").forEach((el) => {
      const key = el.getAttribute("data-i18n");
      if (key && dict[key] != null) el.textContent = dict[key];
    });
    document.querySelectorAll("[data-i18n-placeholder]").forEach((el) => {
      const key = el.getAttribute("data-i18n-placeholder");
      if (key && dict[key] != null) el.setAttribute("placeholder", dict[key]);
    });
    document.querySelectorAll("[data-i18n-aria]").forEach((el) => {
      const key = el.getAttribute("data-i18n-aria");
      if (key && dict[key] != null) el.setAttribute("aria-label", dict[key]);
    });
    const banner = document.querySelector("[data-i18n-banner]");
    if (banner) {
      const text = dict["i18n.banner"] || "";
      banner.hidden = !text;
      banner.textContent = text;
    }
    document.querySelectorAll("[data-lang-option]").forEach((btn) => {
      btn.setAttribute("aria-pressed", btn.getAttribute("data-lang-option") === next ? "true" : "false");
    });
  };

  document.querySelectorAll("[data-lang-option]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const next = btn.getAttribute("data-lang-option");
      if (!supported.includes(next)) return;
      const url = new URL(location.href);
      url.searchParams.set("lang", next);
      history.replaceState({}, "", url);
      apply(next);
    });
  });

  apply(lang);
})();
