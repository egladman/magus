// agents.ts - who is driving magus right now, and through which tool.
//
// magus is increasingly operated by agents rather than by hands: every Claude Code, Codex, Cursor
// and OpenCode session funnels its shell and file-edit calls through the same `magus hook` guard,
// and every MCP call goes through the daemon. All of it lands in the activity trail. Until this
// tile, none of it reached the dashboard - the board could tell you the pool was saturated but not
// that three agents were the reason, and it could not show a guard denial at all.
//
// == The metric is "active recently", NOT "running now" ==
//
// A hook fires BEFORE a tool call and there is no matching end signal: no session-open, no
// session-close, nothing that says an agent stopped. So this cannot count concurrent agents, and
// does not claim to. An agent thinking, or waiting on a long build, has an open session and no
// recent events, and is invisible here.
//
// That distinction is why the tile is titled and captioned for a WINDOW rather than showing a bare
// "3 agents" that reads as a live count. A number that looks authoritative and is quietly wrong is
// worse than a slightly clumsier one that is right - especially on a board whose whole job is to be
// trusted at a glance.
//
// The seat grid deliberately echoes the pool tile: one cell per session, the same at-a-glance
// "how much of this is busy" read. They are the two halves of the same question - the pool is
// magus's own capacity, this is the demand arriving at it.

import type { AgentActivityView, AgentHostView, DashboardState } from "../state";
import { Card, h, type Tile } from "./card";
import { fitRows } from "./density";

const SVG_NS = "http://www.w3.org/2000/svg";

// noticeGlyph draws the severity mark that leads a guard-judged row: the same note / warning
// vocabulary a documentation admonition uses.
//
// Colour alone was carrying the whole distinction before, which fails twice over. It is invisible
// to anyone who cannot separate the two hues, and on a wall display several metres away a tinted
// row edge is simply not resolvable - the tile is meant to be readable from across a room. A shape
// survives both, and reads the same way an operator already reads a docs callout.
//
// Stroked in currentColor so the stylesheet owns severity colour in one place.
function noticeGlyph(kind: "deny" | "advise" | "pass"): SVGElement {
  const svg = document.createElementNS(SVG_NS, "svg");
  svg.setAttribute("viewBox", "0 0 16 16");
  svg.setAttribute("class", "console-dashboard-agents__notice");
  svg.setAttribute("aria-hidden", "true");
  svg.setAttribute("fill", "none");
  svg.setAttribute("stroke", "currentColor");
  svg.setAttribute("stroke-width", "1.4");
  svg.setAttribute("stroke-linecap", "round");
  const path = (d: string): void => {
    const p = document.createElementNS(SVG_NS, "path");
    p.setAttribute("d", d);
    svg.appendChild(p);
  };
  if (kind === "deny") {
    // A barred circle: the universal "this was refused" mark, and the only one here that is not a
    // ring plus decoration, so a denial is separable from a note by silhouette alone.
    path("M8 1.6a6.4 6.4 0 1 0 0 12.8A6.4 6.4 0 0 0 8 1.6Z");
    path("M4.6 4.6 11.4 11.4");
    return svg;
  }
  path("M8 1.6a6.4 6.4 0 1 0 0 12.8A6.4 6.4 0 0 0 8 1.6Z");
  if (kind === "advise") {
    // An "i": the docs note mark.
    path("M8 7.2 8 11.3");
    path("M8 4.7 8 4.9");
    return svg;
  }
  // pass: a check inside the ring. Present rather than blank so every row keeps the same left edge
  // and the list does not ripple when a decision changes.
  path("M5.2 8.2 7.2 10.2 10.8 5.9");
  return svg;
}

// Longest a session's seat stays lit after its last observed call. Below the view's own window so a
// seat visibly cools before the session leaves the tile entirely, rather than vanishing at full
// brightness.
const RECENT_MS = 60_000;

// hostLabel renders the wrapper's host id for display. The ids are the ones the guard templates
// send (claude-code, codex, cursor, opencode); anything else passes through, so a host magus has
// never heard of still appears rather than being bucketed away as unknown.
function hostLabel(host: string): string {
  return host === "unattributed" ? "unattributed" : host;
}

export function agentsTile(): Tile {
  const card = new Card("agents", "Orchestration", {
    note: "no agent traffic",
    why:
      "Who is driving magus, what they are doing, and whether the guard stopped anything. This is" +
      " the bridge between a live delegation plan and the targets actually moving through the pool.",
  });

  const posture = h("div", "console-dashboard-orchestration");
  const postureLine = h(
    "strong",
    "console-dashboard-orchestration__line",
    "Waiting for agent activity",
  );
  const postureDetail = h("span", "console-dashboard-orchestration__detail");
  const planButton = document.createElement("button");
  planButton.type = "button";
  planButton.className =
    "pf-v6-c-button pf-m-link pf-m-inline console-dashboard-orchestration__plan";
  planButton.append(h("span", "pf-v6-c-button__text", "Open delegation plan"));
  planButton.addEventListener("click", () =>
    window.dispatchEvent(new CustomEvent("console:open-surface", { detail: { pageId: "plan" } })),
  );
  posture.append(postureLine, postureDetail, planButton);

  const caption = h(
    "p",
    "console-dashboard-activity__caption",
    "Tool calls agents routed through magus in the last 5 minutes. Sessions are counted from" +
      " observed calls, so an agent that is thinking or waiting on a build shows none.",
  );

  const grid = h("div", "console-dashboard-agents__grid");
  grid.setAttribute("aria-label", "Recently active agent sessions");
  const seatKey = h("div", "console-dashboard-agents__key");
  const list = h("ul", "console-dashboard-rowlist");
  const empty = h(
    "p",
    "console-dashboard-row__empty",
    "No agent tool calls recently. Wire the guard hook to see them here.",
  );
  // What agents actually DID, under the per-host totals.
  //
  // The counts alone stop exactly where the interesting question starts: "1 denied" tells you the
  // guard refused something and gives you no way to learn what, which is the only thing anyone
  // would want next. These rows name each recent call and how it was judged.
  const recentHead = h("p", "console-dashboard-agents__subhead", "Recent calls");
  const recentList = h("ul", "console-dashboard-rowlist console-dashboard-agents__recent");
  // The recent list gets its OWN measured box rather than sharing the card body.
  //
  // Two fitRows on one container would each believe they owned the whole height and both trim to
  // it, so the panel would still overflow by whatever the other one drew. Giving this list a
  // wrapper that takes the leftover height means it trims against the space actually remaining
  // after the seat grid and the host rows - which is what made this tile overflow its two-row Big
  // Picture slot by 164px once the list was added.
  const recentWrap = h("div", "console-dashboard-agents__recentwrap");
  recentWrap.append(recentHead, recentList);
  card.body.append(posture, caption, grid, seatKey, list, recentWrap, empty);

  function renderRecent(view: AgentActivityView, now: number): void {
    const calls = view.recent;
    recentHead.hidden = calls.length === 0;
    recentList.replaceChildren(
      ...calls.map((c) => {
        const li = h("li", "console-dashboard-row console-dashboard-agents__call");
        // The decision drives both the glyph and the row accent, so a denial is findable without
        // reading: it is the one line here that might need a human.
        const decision = c.decision === "deny" || c.decision === "advise" ? c.decision : "pass";
        li.dataset.decision = decision;
        li.append(noticeGlyph(decision));
        const tool = c.tool || (c.mcp ? "mcp tool" : "command");
        // The tool name is the link into the full trail entry. A denial here is a summary - what
        // the operator wants next is the event itself, with its command and reason - and this tile
        // is where they meet it first.
        //
        // Keyed on atMs because the trail assigns no id of its own. Epoch milliseconds are unique
        // per producer in practice, and the trail is append-only, so the worst case of a collision
        // is revealing the neighbouring row of the same millisecond rather than a wrong one.
        const link = h("a", "console-dashboard-row__cmd") as HTMLAnchorElement;
        link.href = "../activity/#at=" + String(c.atMs);
        link.append(h("code", undefined, tool));
        li.append(link);
        const meta = h("span", "console-dashboard-row__meta");
        const age = Math.max(0, Math.round((now - c.atMs) / 1000));
        const bits = [c.mcp ? "mcp" : hostLabel(c.host)];
        if (c.decision && c.decision !== "pass") bits.push(c.decision);
        bits.push(age < 60 ? age + "s ago" : Math.round(age / 60) + "m ago");
        meta.textContent = bits.join(" - ");
        li.append(meta);
        return li;
      }),
    );
  }

  // A key for the seat colours, built from the hosts actually present.
  //
  // The seats were coloured by host with nothing anywhere saying which colour meant which agent, so
  // the row of cubes was decorative to everyone but its author. Derived from the live view rather
  // than hardcoded, so it lists the agents actually driving this daemon and cannot drift from the
  // palette in dashboard.css.
  function renderSeatKey(hosts: AgentHostView[]): void {
    seatKey.replaceChildren(
      ...hosts.map((hv) => {
        const item = h("span", "console-dashboard-agents__keyitem", hostLabel(hv.host));
        item.dataset.host = hv.host;
        return item;
      }),
    );
    seatKey.hidden = hosts.length === 0;
  }

  function renderSeats(view: AgentActivityView, now: number): void {
    // One cell per session, coloured by its host, dimmed once its last call is older than RECENT_MS.
    // Capped so a daemon serving a swarm cannot grow the DOM without bound; the residual rides the
    // header note, which never truncates.
    const cells: HTMLElement[] = [];
    for (const hostView of view.hosts) {
      for (let i = 0; i < hostView.sessions && cells.length < 64; i++) {
        const cell = h("div", "console-dashboard-agents__seat");
        cell.dataset.host = hostView.host;
        cell.dataset.warm = now - hostView.lastMs < RECENT_MS ? "yes" : "no";
        cell.title = hostLabel(hostView.host) + " - " + hostView.calls + " calls";
        cells.push(cell);
      }
    }
    grid.replaceChildren(...cells);
    grid.hidden = cells.length === 0;
  }

  function renderHosts(hosts: AgentHostView[]): void {
    list.replaceChildren(
      ...hosts.map((hv) => {
        const li = h("li", "console-dashboard-row console-dashboard-agents__call");
        // Same severity vocabulary as the per-call rows below, so one glyph means one thing
        // everywhere in the tile. A host's severity is the worst judgement any of its calls got.
        const sev = hv.denied > 0 ? "deny" : hv.advised > 0 ? "advise" : "pass";
        li.dataset.decision = sev;
        li.append(noticeGlyph(sev));
        li.append(h("code", "console-dashboard-row__cmd", hostLabel(hv.host)));
        const meta = h("span", "console-dashboard-row__meta");
        const bits = [
          hv.sessions + (hv.sessions === 1 ? " session" : " sessions"),
          hv.calls + " calls",
        ];
        // Denials lead when present: a blocked command is the one entry here that may need a human,
        // and burying it behind the counts would make the tile a traffic meter rather than a signal.
        if (hv.denied > 0) bits.push(hv.denied + " denied");
        if (hv.advised > 0) bits.push(hv.advised + " advised");
        meta.textContent = bits.join(" - ");
        li.append(meta);
        return li;
      }),
    );
  }

  function render(view: AgentActivityView | null): void {
    // Hidden entirely until there is something to say. The board already has plenty to read, and a
    // permanently empty "0 agents" tile on a machine nobody drives with an agent is pure noise.
    const has = !!view && (view.totalCalls > 0 || view.mcpCalls > 0);
    card.el.hidden = !has;
    empty.hidden = has;
    if (!view || !has) {
      card.setNote("no agent traffic");
      grid.hidden = true;
      list.replaceChildren();
      recentList.replaceChildren();
      recentHead.hidden = true;
      return;
    }
    const now = Date.now();
    const activeHosts = view.hosts.filter((host) => now - host.lastMs < RECENT_MS);
    posture.dataset.state =
      view.denied > 0 ? "attention" : activeHosts.length > 0 ? "active" : "idle";
    postureLine.textContent =
      view.denied > 0
        ? view.denied +
          (view.denied === 1 ? " guarded action needs review" : " guarded actions need review")
        : activeHosts.length > 0
          ? activeHosts.length +
            (activeHosts.length === 1
              ? " agent host coordinating work"
              : " agent hosts coordinating work")
          : "Recent agent activity, no active calls";
    postureDetail.textContent =
      view.totalSessions +
      (view.totalSessions === 1 ? " session" : " sessions") +
      " across " +
      view.hosts.length +
      (view.hosts.length === 1 ? " host" : " hosts");
    renderSeats(view, now);
    renderSeatKey(view.hosts);
    renderHosts(view.hosts);
    renderRecent(view, now);
    const parts = [
      view.totalSessions + (view.totalSessions === 1 ? " session" : " sessions"),
      view.totalCalls + " calls",
    ];
    if (view.mcpCalls > 0) parts.push(view.mcpCalls + " MCP");
    if (view.denied > 0) parts.push(view.denied + " denied");
    card.setNote(parts.join(" / "));
  }

  // Trim the host list to the slot, like every other list on the board. The tile is the shortest
  // panel in Big Picture's bottom rail, so on a daemon serving several hosts the list is exactly
  // the thing that would otherwise be silently cut off - and a truncated list of WHO is driving
  // magus is the kind of omission that reads as "only these two".
  const unfit = fitRows(recentWrap, recentList, (hidden) => "+" + String(hidden) + " more");

  return {
    el: card.el,
    update(s: DashboardState) {
      render(s.agents);
    },
    destroy() {
      unfit();
    },
  };
}
