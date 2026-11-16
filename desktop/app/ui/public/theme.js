// Theme before first paint (an external file: the page's CSP allows no inline script). Keep the key in step
// with @stage/app's src/lib/theme.ts.
try {
  var t = localStorage.getItem("stage-theme");
  if (t === "light" || t === "dark") document.documentElement.setAttribute("data-theme", t);
} catch {}
