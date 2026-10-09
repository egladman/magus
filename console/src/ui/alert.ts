// alert.ts - the PF inline Alert: the in-place half of "every failure is a toast plus text where the
// reader is looking". lib/toast.ts is the toast half and builds on the same parts.

import { statusGlyph } from "./status";

export type AlertVariant = "info" | "success" | "warning" | "danger" | "custom";

export interface InlineAlertOptions {
  variant: AlertVariant;
  title: string;
  // A string becomes a paragraph; a Node is appended as given.
  body?: string | Node;
  // Inline link buttons (`pf-v6-c-button pf-m-link pf-m-inline`), the only action PF's alert allows.
  actions?: HTMLElement[];
  // Announce when inserted. Off by default: an alert already on the page at load is read in place.
  // A danger alert is always announced, since an error the reader cannot hear is not reported.
  live?: boolean;
}

const NAMES: Record<AlertVariant, string> = {
  info: "Info",
  success: "Success",
  warning: "Warning",
  danger: "Danger",
  custom: "Custom",
};

// alertName is the severity word a screen reader hears before the title, e.g. "Danger alert".
export function alertName(variant: AlertVariant): string {
  return NAMES[variant] + " alert";
}

// alertIcon wraps the status svg in PF's alert icon slot, which supplies the variant colour.
export function alertIcon(variant: AlertVariant): HTMLElement {
  const icon = document.createElement("div");
  icon.className = "pf-v6-c-alert__icon";
  icon.append(statusGlyph(variant));
  return icon;
}

// alertTitle is the title with its screen-reader severity prefix. The prefix and the title share
// one element so the alert reads "Danger alert: <title>".
export function alertTitle(variant: AlertVariant, title: string): HTMLElement {
  const el = document.createElement("p");
  el.className = "pf-v6-c-alert__title";
  const prefix = document.createElement("span");
  prefix.className = "pf-v6-screen-reader";
  prefix.textContent = alertName(variant) + ":";
  el.append(prefix, " ", title);
  return el;
}

// inlineAlert builds a PF inline alert. The status icon is aria-hidden, so the severity reaches a
// reader that cannot see the colour through the prefix, and the shape reaches one that cannot tell
// the hues apart.
export function inlineAlert(opts: InlineAlertOptions): HTMLElement {
  const alert = document.createElement("div");
  alert.className = "pf-v6-c-alert pf-m-inline pf-m-" + opts.variant;
  alert.setAttribute("aria-label", alertName(opts.variant));
  if (opts.variant === "danger") alert.setAttribute("role", "alert");
  else if (opts.live) alert.setAttribute("role", "status");

  alert.append(alertIcon(opts.variant), alertTitle(opts.variant, opts.title));

  if (opts.body !== undefined) {
    const description = document.createElement("div");
    description.className = "pf-v6-c-alert__description";
    if (typeof opts.body === "string") {
      const p = document.createElement("p");
      p.textContent = opts.body;
      description.append(p);
    } else {
      description.append(opts.body);
    }
    alert.append(description);
  }

  if (opts.actions && opts.actions.length > 0) {
    const group = document.createElement("div");
    group.className = "pf-v6-c-alert__action-group";
    group.append(...opts.actions);
    alert.append(group);
  }
  return alert;
}
