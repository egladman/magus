import type { ServerNeed } from "../desktop/connectPrompt";

// A glyph's one-shot hover motion (the @keyframes in console.css). A part motion rides the one
// inner element of the glyph carrying data-motion="<kind>"; a whole motion turns the entire glyph,
// so the launcher sets it on the icon slot instead.
export type PartMotion = "pulse" | "bars" | "wave" | "sweep" | "press";
export type WholeMotion = "gear" | "settle";

// How the shell loads an app. The build keys on the literal `kind: "shell"` in app.ts to skip the
// app's bundle, so spell it that way.
export type AppLoad =
  // Registered from console.js itself, because it drives state the shell owns (the live keymap,
  // the command list) that a separate bundle would get its own copy of.
  | { kind: "shell" }
  // gen/<id>/<id>.js, whose activate(host) builds the app's DOM.
  | { kind: "module"; css: string }
  // gen/<id>/<id>.js booted over gen/<id>/scaffold.html, the app's <main>.
  | { kind: "page"; css: string };

// AppManifest is one launcher tile. `css` paths are under gen/.
export interface AppManifest {
  // The registry id: tabs, tiling, the command palette's console.open.<id> and ?app=<id> key on it.
  id: string;
  label: string;
  hint: string;
  // Inner markup of a 24x24 stroked svg, used for both the tile icon and its watermark.
  glyph: string;
  motion: PartMotion | WholeMotion | null;
  // The clean deep-link segment, /console/<path>/, or null for an app that has none.
  path: string | null;
  // More served segments that open this app in one of its modes, keyed by segment.
  modes?: Readonly<Record<string, string>>;
  // The palette slot tinting the tile; none inherits the shared accent.
  accent?: string;
  // Consulted rather than worked in: the rail pins these to its foot.
  utility?: boolean;
  // Set on an app with nothing to show without a server; the shell shows its connect page instead.
  server?: ServerNeed;
  load: AppLoad;
}

export const WHOLE_MOTIONS: readonly WholeMotion[] = ["gear", "settle"];

export function isWholeMotion(m: AppManifest["motion"]): m is WholeMotion {
  return WHOLE_MOTIONS.includes(m as WholeMotion);
}
