// share.ts - the toolbar "actions" that hand a loaded log off elsewhere: Share re-encodes the
// exact loaded structure into a #data= link (so a run's output travels without re-running
// magus), and Open in graph jumps to the target's knowledge-graph node. Both are local: Share
// copies to the clipboard, the graph link rides the graph page's own #node= fragment.

import { create, toBinary } from "@bufbuild/protobuf";
import { EventSchema, JournalSchema, Kind } from "../../gen/magus/viewer/v1/viewer_pb";
import { state } from "./state";
import { flashBtnLabel } from "./dom";
import { encodeFragmentBytes } from "./fragment";

// --- Share: re-encode the loaded log into a #data= link -----------------------
// The Share button rebuilds the exact fragment link the viewer decodes and copies it to the
// clipboard, so a run's output can be handed off without re-running magus. The payload is the
// SAME format loadFromURL reads: toBinary(JournalSchema, ...) of a Journal, gzip+base64url.
// A structured log ships its real Journal; a heuristic/pasted log is wrapped into a minimal
// Journal (one KindOutput event per line) so the link still round-trips the structured path.
export async function shareLink(btn: HTMLElement | null): Promise<void> {
  try {
    const bytes = shareBytes();
    const blob = await encodeFragmentBytes(bytes);
    // Build the base from origin+pathname so any existing fragment/query is dropped; the
    // whole link then rides the fragment, which the browser never transmits to a server.
    const base = location.origin + location.pathname;
    const frag = (state.currentRef ? "ref=" + state.currentRef + "&" : "") + "data=" + blob;
    const url = base + "#" + frag;
    if (navigator.clipboard && navigator.clipboard.writeText) {
      await navigator.clipboard.writeText(url);
      flashBtnLabel(btn, "Copied");
    } else {
      flashBtnLabel(btn, "Failed");
    }
  } catch (_) {
    flashBtnLabel(btn, "Failed");
  }
}

// shareBytes serializes the loaded log to the Journal wire bytes the viewer decodes: the real
// Journal when one is loaded, else a minimal Journal synthesized from the rendered lines (the
// same flat lines the RAW view shows) so a text/pasted log still round-trips as structure.
function shareBytes(): Uint8Array {
  if (state.currentJournal) return toBinary(JournalSchema, state.currentJournal);
  const lines = state.rawLines || (state.model ? state.model.sections.flatMap((s) => s.lines) : []);
  const events = lines.map((text) => create(EventSchema, { kind: Kind.OUTPUT, text }));
  const journal = create(JournalSchema, { events });
  return toBinary(JournalSchema, journal);
}

// --- Open in graph: jump to the target's knowledge-graph node -----------------
// graphTarget reads the (project, target) off the loaded Journal's result event - the pair
// that names a target node. Only meaningful with a real ref seed; returns null otherwise, so
// the button stays hidden for pasted/live logs that do not identify a graph target.
export function graphTarget(): { project: string; target: string } | null {
  if (!state.currentRef || !state.currentJournal) return null;
  for (const ev of state.currentJournal.events || []) {
    if (ev.kind === Kind.RESULT && ev.project && ev.target) return { project: ev.project, target: ev.target };
  }
  return null;
}

// openInGraph builds the knowledge-graph node id exactly as internal/knowledge/id.go targetID
// spells it ("target:<project>:<target>") and opens the graph explorer on that node in a new
// tab. The node id rides the graph page's own #node= fragment, so nothing leaves the machine.
export function openInGraph(): void {
  const t = graphTarget();
  if (!t) return;
  const nodeId = "target:" + t.project + ":" + t.target;
  window.open("../graph/#node=" + encodeURIComponent(nodeId), "_blank");
}
