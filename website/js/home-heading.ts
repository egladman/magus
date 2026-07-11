// home-heading.ts - relabel the home page's top heading at runtime.
//
// The landing page is rendered from README.md, whose first heading is "# magus",
// so the article's <h1> reads "magus". On the home page (index.html) only, swap
// that heading's text to "Readme" - without touching the README source or the
// other places the title is derived from (the nav brand, <title>, breadcrumb,
// and the search index all keep saying "magus").
(function () {
  // Only the generated index.html (served at gen/'s root, as a directory URL, or
  // explicitly as .../index.html).
  const onHome = /(^|\/)index\.html$/.test(location.pathname) || /\/$/.test(location.pathname);
  if (!onHome) return;
  const h1 = document.querySelector("main article h1");
  if (h1 && h1.textContent?.trim() === "magus") h1.textContent = "Readme";
})();
