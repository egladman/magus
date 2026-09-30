import type { AppManifest } from "../manifest";

export const notes: AppManifest = {
  id: "notes",
  label: "Notes",
  // Not "what people wrote about this workspace", which describes the storage: a note is left for
  // whoever comes next, at the spot where it matters.
  hint: "What people left here for whoever comes next",
  // A page lying ASKEW, the one glyph not square to the grid, because a note is the one thing a
  // person put there by hand. Page and prose settle together, so the motion turns the whole glyph.
  glyph:
    '<path d="M13.6 3.1 7.4 4.8a2 2 0 0 0-1.4 2.45l3 11a2 2 0 0 0 2.45 1.4l6.8-1.85a2 2 0 0 0 1.4-2.45L17.2 6.6z"/><path d="m13.6 3.1 1 3.6 3.6-1"/><path d="m10.5 11.8 5-1.35"/><path d="m11.4 15.1 3.4-.9"/>',
  motion: "settle",
  path: "notes",
  // Clay, not a green: the greens mean live/healthy, and a note is not a status.
  accent: "--console-clay",
  server: {
    purpose: "Notes are prose a person wrote about this workspace, anchored to what it is about.",
  },
  load: { kind: "module", css: "notes/notes.css" },
};
