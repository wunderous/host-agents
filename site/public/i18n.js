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
    fr: {
      "nav.docs": "Documentation",
      "nav.getStarted": "Commencer",
      "nav.search": "Rechercher dans la documentation",
      "search.placeholder": "Rechercher dans la documentation…",
      "search.empty": "Aucun résultat.",
      "lang.label": "Langue",
      "footer.facts": "Les informations reflètent le dépôt",
      "footer.openapi": "OpenAPI",
      "i18n.banner":
        "L’interface est en français. Le contenu des pages reste en anglais pour le moment.",
    },
  };

  const supported = Object.keys(STRINGS);
  window.oputeDocsI18n = {
    translate: (key) => {
      const dict = STRINGS[document.documentElement.lang] || STRINGS.en;
      return dict[key] ?? STRINGS.en[key] ?? "";
    },
  };
  const params = new URLSearchParams(location.search);
  const stored = localStorage.getItem("opute-docs-lang");
  const requested = params.get("lang") || stored || document.documentElement.lang || "en";
  const initial = requested === "es" ? "fr" : requested;
  if (params.get("lang") === "es") {
    const url = new URL(location.href);
    url.searchParams.set("lang", "fr");
    history.replaceState({}, "", url.toString());
  }
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
