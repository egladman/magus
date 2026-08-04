// copy-markdown.ts - adds a "Copy Markdown" control to the .page-tools-secondary row,
// right after "View markdown source" (page.buzz's page-tools-edit link). Progressive
// enhancement only: JS-inserted, so a page with JS disabled keeps just the two
// server-rendered links and no trace of this one. On click it fetches the page's own
// index.md (the same same-origin URL "View markdown source" points at) and copies the
// raw text to the clipboard, swapping the button's own label to confirm success or
// failure. No fetch or clipboard permission prompt happens until the reader clicks.

const REST_LABEL = "Copy Markdown";
const DONE_LABEL = "Copied";
const FAIL_LABEL = "Copy failed";

export function initCopyMarkdown(): void {
  if (!navigator.clipboard) return;
  const sourceLink = document.querySelector<HTMLAnchorElement>(
    ".page-tools-edit a[href='index.md']",
  );
  if (!sourceLink) return;

  const btn = document.createElement("button");
  btn.type = "button";
  btn.className = "page-tools-copy";
  btn.textContent = REST_LABEL;
  btn.setAttribute("data-tooltip", "Copy this page's Markdown source");
  btn.title = "Copy this page's Markdown source";

  // Insert right after the source link, ahead of the existing " &middot; " that already
  // separates it from "Suggest an edit" - both calls target sourceLink itself, so the
  // text lands between the two anchors and the button lands between the text and the
  // link it now precedes.
  sourceLink.insertAdjacentElement("afterend", btn);
  sourceLink.insertAdjacentText("afterend", " · ");

  let timer: ReturnType<typeof setTimeout> | undefined;
  function reset(): void {
    btn.textContent = REST_LABEL;
  }
  function flash(label: string): void {
    btn.textContent = label;
    if (timer) clearTimeout(timer);
    timer = setTimeout(reset, 2000);
  }

  btn.addEventListener("click", () => {
    fetch(sourceLink.href)
      .then((r) => {
        if (!r.ok) throw new Error("markdown fetch HTTP " + r.status);
        return r.text();
      })
      .then((text) => navigator.clipboard.writeText(text))
      .then(
        () => flash(DONE_LABEL),
        () => flash(FAIL_LABEL),
      );
  });
}
