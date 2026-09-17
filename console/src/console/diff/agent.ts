// agent.ts - the session panel behind a story row: what the agent ran, the turns leading up to
// its write, and a plain statement of every transcript part the daemon could not show.

import { createClient, ConnectError } from "@connectrpc/connect";
import {
  ViewerService,
  TurnRole,
  type SessionActivity,
  type SessionTurn,
} from "@wire/viewer/v1alpha1/viewer_pb";
import { createDaemonTransport, getLiveToken } from "../../lib/daemon";
import { h } from "../view";
import type { DiffTouch } from "./session";

export type ActivityResult =
  | { readonly activity: SessionActivity }
  | { readonly failed: string }
  // The showcase has no daemon, so the panel renders the touch alone and says why.
  | { readonly offline: string };

export async function fetchSessionActivity(
  host: string,
  session: string,
  path: string,
  signal: AbortSignal,
): Promise<ActivityResult> {
  try {
    const client = createClient(ViewerService, createDaemonTransport(host, getLiveToken()));
    return { activity: await client.getSessionActivity({ session, path }, { signal }) };
  } catch (e) {
    return { failed: e instanceof ConnectError ? e.rawMessage : String(e) };
  }
}

const ROLE_NAMES: Record<TurnRole, string> = {
  [TurnRole.UNSPECIFIED]: "turn",
  [TurnRole.USER]: "user prompts",
  [TurnRole.ASSISTANT]: "assistant text",
  [TurnRole.REASONING]: "reasoning",
  [TurnRole.TOOL]: "tool calls",
};

function turnLabel(turn: SessionTurn): string {
  switch (turn.role) {
    case TurnRole.USER:
      return "user";
    case TurnRole.ASSISTANT:
      return "assistant";
    case TurnRole.REASONING:
      return "reasoning";
    default:
      return turn.kind || "tool";
  }
}

function turnText(turn: SessionTurn): string {
  if (turn.kind === "shell.command") return turn.program || "(program not recorded)";
  if (turn.kind === "hook.output") return "guard output (text withheld)";
  return turn.text;
}

function renderTurn(turn: SessionTurn): HTMLElement {
  const li = h("li", "console-diff-agent__turn");
  li.dataset.role = TurnRole[turn.role].toLowerCase();
  li.append(h("span", "console-diff-agent__role", turnLabel(turn)));
  li.append(h("span", "console-diff-agent__text", turnText(turn)));
  const notes: string[] = [];
  if (turn.verdict && turn.verdict !== "pass") notes.push(`guard: ${turn.verdict}`);
  if (turn.denied) notes.push("denied by host");
  if (turn.interrupted) notes.push("interrupted");
  if (turn.exit !== 0) notes.push(`exit ${turn.exit}`);
  if (notes.length) li.append(h("span", "console-diff-agent__note", notes.join(", ")));
  return li;
}

// unavailable is the sentence for a part the panel cannot show. Every part is stated, because a
// panel that quietly lacks reasoning reads as an agent that did not reason.
function unavailable(result: ActivityResult, role: TurnRole): string | null {
  if ("activity" in result) {
    if (result.activity.turns.some((t) => t.role === role)) return null;
    const why = result.activity.unrecorded.find((u) => u.role === role)?.reason;
    return `${capitalize(ROLE_NAMES[role])} unavailable for this session: ${why || "its record holds none"}.`;
  }
  const why = "offline" in result ? result.offline : `the daemon did not answer (${result.failed})`;
  return `${capitalize(ROLE_NAMES[role])} unavailable for this session: ${why}.`;
}

function capitalize(s: string): string {
  return s.charAt(0).toUpperCase() + s.slice(1);
}

// renderAgentSession fills body with the session behind one touch. result is null while the
// daemon's answer is in flight.
export function renderAgentSession(
  body: HTMLElement,
  touch: DiffTouch,
  result: ActivityResult | null,
): void {
  body.replaceChildren();

  const ran = h("section", "console-diff-agent__ran");
  ran.append(h("h4", "console-diff-agent__heading", "Ran before this write"));
  if (touch.ran?.length) {
    const list = h("ul", "console-diff-agent__programs");
    // The wire carries newest first; a reader follows a session forwards.
    for (const program of [...touch.ran].reverse())
      list.append(h("li", "console-diff-agent__program", program));
    ran.append(list);
  } else {
    ran.append(h("p", "console-diff-agent__empty", "No commands recorded before this write."));
  }
  body.append(ran);

  const turns = h("section", "console-diff-agent__turns");
  turns.append(h("h4", "console-diff-agent__heading", "Transcript"));
  body.append(turns);
  if (!result) {
    turns.append(h("p", "console-diff-agent__empty", "Loading session..."));
    return;
  }

  const notices = h("ul", "console-diff-agent__unavailable");
  for (const role of [TurnRole.USER, TurnRole.ASSISTANT, TurnRole.REASONING]) {
    const sentence = unavailable(result, role);
    if (sentence) notices.append(h("li", "console-diff-agent__notice", sentence));
  }
  turns.append(notices);

  if (!("activity" in result)) return;
  const { activity } = result;
  if (!activity.wrote) {
    turns.append(
      h(
        "p",
        "console-diff-agent__empty",
        "No loaded transcript holds this write. Run the host's session load adapter to load it.",
      ),
    );
    return;
  }
  if (activity.truncated)
    turns.append(
      h("p", "console-diff-agent__empty", "Earlier turns before this write were left out."),
    );
  const list = h("ol", "console-diff-agent__list");
  for (const turn of activity.turns) list.append(renderTurn(turn));
  turns.append(list);
}
