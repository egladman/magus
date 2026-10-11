// empty-state.ts - PatternFly's EmptyState structure, built once: the content column, a header holding
// the status icon (__icon) over the title (__title > __title-text), the body, and a footer holding the
// actions (__footer > __actions). Every empty state in the console is this shape; the app fills the
// parts it has.

import { h } from "../desktop/view";

export interface EmptyStateOptions {
  // Extra classes for the root, such as an app's own hook or a PF size modifier.
  classes?: string;
  // The title's heading level follows the page it sits in.
  heading: "h1" | "h2" | "h3";
  title?: string;
  // The status glyph. It goes in __icon, which hides it from assistive technology: the title and body
  // already say what the icon shows.
  icon?: Node;
  // Whether the actions group is a row of the console's "ways" cards (data-empty-ways).
  ways?: boolean;
}

export interface EmptyState {
  root: HTMLElement;
  header: HTMLElement;
  // The __icon box, or null when the state carries no icon.
  icon: HTMLElement | null;
  title: HTMLElement;
  body: HTMLElement;
  footer: HTMLElement;
  actions: HTMLElement;
}

export function emptyStateShell(o: EmptyStateOptions): EmptyState {
  const root = h("div", "pf-v6-c-empty-state" + (o.classes ? " " + o.classes : ""));
  const content = h("div", "pf-v6-c-empty-state__content");
  const header = h("div", "pf-v6-c-empty-state__header");
  let icon: HTMLElement | null = null;
  if (o.icon) {
    icon = h("div", "pf-v6-c-empty-state__icon");
    icon.setAttribute("aria-hidden", "true");
    icon.append(o.icon);
    header.append(icon);
  }
  const titleBox = h("div", "pf-v6-c-empty-state__title");
  const title = h(o.heading, "pf-v6-c-empty-state__title-text", o.title);
  titleBox.append(title);
  header.append(titleBox);
  const body = h("div", "pf-v6-c-empty-state__body");
  const footer = h("div", "pf-v6-c-empty-state__footer");
  const actions = h("div", "pf-v6-c-empty-state__actions");
  if (o.ways) actions.dataset.emptyWays = "";
  footer.append(actions);
  content.append(header, body, footer);
  root.append(content);
  return { root, header, icon, title, body, footer, actions };
}
