// Renders the shell-owned status bar from one contribution shape.

import { parseHash, wantsDemo } from "../lib/daemon";

export type ConnectionState = "none" | "connecting" | "connected" | "disconnected" | "demo";

// DEMO_HINT is the sentence the readiness poller and makeStatusBar also write, kept identical so
// the tooltip does not change wording depending on which of the three last touched it.
const DEMO_HINT = "Demo data is synthetic. Click to change the daemon address.";

export interface StatusContribution {
  // connection/label are for a surface that maintains its OWN link to the daemon - the graph's SSE
  // stream, the log tail. Omit both when the surface has no link of its own: the shell's readiness
  // poller then answers, which is the only answer a surface without a connection could honestly
  // give anyway. Passing one requires the other; a state with no words is a colored dot that says
  // nothing, and words with no state color them wrong.
  connection?: ConnectionState;
  label?: string;
  health?: string;
  hint?: string;
  count?: string;
  observing?: { text: string; title: string };
}

// publishStatus writes one surface's contribution into the shell's status bar.
//
// The CONNECTION half is overridden in demo mode, and that override is the point: a surface reports
// the link IT believes it has, and in demo mode several believed different things. The log viewer
// published "connected" - a daemon link that does not exist - while the graph explorer published
// "not connected" and the dashboard published "demo", so one console said three things about
// itself depending on which tab was in front. The fragment is the authority on demo everywhere else
// in the shell (the readiness pulse, the connect screen, makeStatusBar), so it is the authority
// here, and no surface can contradict it.
//
// Only the connection state is taken over. count and observing describe the DATA a surface is
// showing rather than the link behind it, so they stay the surface's to report.
//
// A contribution that carries a connection CLAIMS the connection slot, stamped on the bar element
// so the shell's readiness poller stops writing to it. The bars are per-tab, so the claim is too:
// a tab whose surface never claims keeps the poller's answer instead of makeStatusBar's
// construction-time "not connected", which was a live console reporting no daemon on every surface
// that had nothing to say.
export function publishStatus(contribution: StatusContribution): void {
  const demoing = wantsDemo(parseHash());
  const conn = document.getElementById("console-conn");
  if (conn && contribution.connection) {
    conn.dataset.owner = "surface";
    conn.textContent = demoing ? "demo" : (contribution.label ?? "");
    conn.dataset.state = demoing ? "demo" : contribution.connection;
    if (contribution.health && !demoing) conn.dataset.health = contribution.health;
    else delete conn.dataset.health;
    const hint = demoing ? DEMO_HINT : contribution.hint;
    if (hint !== undefined) {
      conn.title = hint;
      conn.setAttribute("aria-label", hint);
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
