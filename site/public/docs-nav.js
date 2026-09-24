(() => {
  const navigation = document.querySelector(".doc-nav-toggle");
  if (!navigation || typeof window.matchMedia !== "function") return;

  const mobile = window.matchMedia("(max-width: 879px)");
  const sync = () => {
    navigation.open = !mobile.matches;
  };

  sync();
  mobile.addEventListener?.("change", sync);
})();
