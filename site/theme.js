(function () {
  var KEY = "bgmcat-site-theme";
  var mode = "system";
  try {
    var saved = localStorage.getItem(KEY);
    if (saved === "dark" || saved === "light" || saved === "system") mode = saved;
  } catch (_) {}

  function apply(next) {
    mode = next;
    var dark =
      mode === "dark" ||
      (mode === "system" && window.matchMedia && window.matchMedia("(prefers-color-scheme: dark)").matches);
    document.documentElement.dataset.theme = dark ? "dark" : "light";
    document.documentElement.dataset.themeMode = mode;
  }

  apply(mode);

  if (window.matchMedia) {
    window.matchMedia("(prefers-color-scheme: dark)").addEventListener("change", function () {
      if (mode === "system") apply("system");
    });
  }

  window.BgmcatTheme = {
    getMode: function () {
      return mode;
    },
    cycle: function () {
      var next = mode === "system" ? "dark" : mode === "dark" ? "light" : "system";
      try {
        localStorage.setItem(KEY, next);
      } catch (_) {}
      apply(next);
      return next;
    }
  };
})();
