// model.ts - the render model shared by every console surface that shows foldable,
// status-accented sections of text: the log viewer (a run's captured output) and the
// activity view (the daemon's audit trail). It is the neutral shape both the log viewer's
// event/text parsers and the activity adapter produce, so the same DOM renderer
// (sections.ts) paints them identically. Pure types, no DOM, no imports.

// A foldable section of the view: a header line + its body lines. meta carries the
// structured (label, status) the log viewer's filter matches against; the activity view
// leaves it unset. The first body line is conventionally the header line repeated.
export interface Section {
  title: string | null;
  lines: string[];
  meta?: { label: string; status: string };
}

// The render model: the sections plus a count of titled (headed) ones.
export interface RenderModel {
  sections: Section[];
  titled: number;
}
