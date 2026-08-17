// Renders the shell-owned status bar from one contribution shape.

export type ConnectionState = "none" | "connecting" | "connected" | "disconnected" | "demo";

export interface StatusContribution {
  connection: ConnectionState;
  label: string;
  health?: string;
  hint?: string;
  count?: string;
  observing?: { text: string; title: string };
}

export function publishStatus(contribution: StatusContribution): void {
  const conn = document.getElementById("console-conn");
  if (conn) {
    conn.textContent = contribution.label;
    conn.dataset.state = contribution.connection;
    if (contribution.health) conn.dataset.health = contribution.health;
    else delete conn.dataset.health;
    if (contribution.hint !== undefined) {
      conn.title = contribution.hint;
      conn.setAttribute("aria-label", contribution.hint);
    }
  }
  const count = document.getElementById("console-count");
  if (count) {
    count.textContent = contribution.count ?? "";
    count.hidden = !contribution.count;
  }
  const observing = document.getElementById("console-observing");
  if (observing) {
    observing.textContent = contribution.observing?.text ?? "";
    observing.title = contribution.observing?.title ?? "";
    observing.hidden = !contribution.observing;
  }
}
