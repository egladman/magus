// density.ts - the compaction contract for row lists.
//
// A tile on the board sits in a document that scrolls, so a list longer than its card is a
// non-event: the reader scrolls. Big Picture has no scrollbar and frequently nobody near enough to
// use one, so the same list silently loses its tail behind `overflow: hidden`. That failure is
// worse than showing less, because the display gives the reader no way to tell "nothing else" from
// "plenty else" - a wall board reading "2 workspaces" when nine are loaded is not a smaller truth,
// it is a wrong one.
//
// So: render every row, then trim to what actually fits and spend the last line saying what was
// dropped. The residual line is not optional decoration; it is the entire reason this exists.
//
// Measured, not budgeted from constants. Row height depends on the fluid type scale, which depends
// on the viewport, so any hardcoded row count would be wrong at some size. A ResizeObserver on the
// container re-fits whenever the slot changes - entering the mode, rotating a tablet, resizing a
// window, or a font finishing loading.

// fitRows trims list's children to what container can show and appends one residual line.
//
// `label` renders the residual text from the number of hidden rows, so a caller can say something
// truer than a bare count when it knows more ("+6 more, 2 failing"). Returning "" suppresses the
// line entirely, which is the right answer when nothing was hidden.
//
// Rows are measured one by one, not as copies of the first: a row that wraps onto a second line
// (a lock's holder directory) is taller than its neighbours, and counting it as one line shows more
// rows than the slot holds.
//
// Returns a teardown that disconnects the observer. Callers hold it and call it from destroy().
export function fitRows(
  container: HTMLElement,
  list: HTMLElement,
  label: (hidden: number, rows: HTMLElement[]) => string,
): () => void {
  const residual = document.createElement("li");
  residual.className = "console-dashboard-row__residual";
  residual.hidden = true;

  function fit(): void {
    // Take the residual out of the measurement before measuring: leaving it in makes the fit
    // depend on its own previous outcome, which oscillates by a row on every other pass.
    residual.remove();
    const rows = [...list.children] as HTMLElement[];
    for (const r of rows) r.hidden = false;
    if (rows.length === 0) return;

    const available = container.clientHeight;
    // clientHeight is 0 while the tile is display:none (the board hides the whole Big Picture set,
    // and vice versa). Fitting against 0 would hide every row and then leave them hidden once the
    // tile came back, since nothing re-measures on an unhide. Bail and let the observer re-fire.
    if (available <= 0) return;

    let used = 0;
    let capacity = 0;
    for (const row of rows) {
      const height = row.getBoundingClientRect().height;
      if (height <= 0) return;
      if (used + height > available) break;
      used += height;
      capacity++;
    }
    if (capacity >= rows.length) return; // everything fits: no trim, no residual line

    // Spend one slot on the residual line itself. Math.max(0, ...) covers a container too short for
    // even one row: better to show only the residual ("+9 more") than to show nothing at all and
    // read as an empty list.
    const keep = Math.max(0, capacity - 1);
    const hidden = rows.slice(keep);
    for (const r of hidden) r.hidden = true;

    const text = label(hidden.length, hidden);
    if (!text) return;
    residual.textContent = text;
    residual.hidden = false;
    list.append(residual);
  }

  // Observe the CONTAINER, not the list: the list's own height is an output of this function, so
  // observing it would feed every trim back in as a new resize and loop.
  const obs = new ResizeObserver(fit);
  obs.observe(container);
  return () => obs.disconnect();
}
