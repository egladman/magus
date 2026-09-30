import type { AppManifest } from "../manifest";

export const shortcuts: AppManifest = {
  id: "shortcuts",
  // Not "Actions", which collides with the Command Palette and the Activity feed. VS Code's
  // Keyboard Shortcuts editor is the prior art: every command with its binding. The palette runs
  // one.
  label: "Shortcuts",
  hint: "Every command, its keys, and where to change them",
  // A keyboard, not a lightning bolt, which reads as "fast", the palette's job. The spacebar
  // presses on hover.
  glyph:
    '<rect x="2" y="6" width="20" height="12" rx="2"/><path d="M6 10h.01M10 10h.01M14 10h.01M18 10h.01"/><path data-motion="press" d="M8.5 14h7"/>',
  motion: "press",
  path: null,
  accent: "--console-gold",
  utility: true,
  load: { kind: "shell" },
};
