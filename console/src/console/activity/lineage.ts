// lineage.ts - groups a page of activity events by the agent SESSION that produced them, and joins
// a spawning session to the sessions its spawn handed a lease to. The trail records one flat
// time-ordered list, so the fan-out an orchestrator produced reads as interleaved rows from
// unrelated agents; this recovers the shape from fields already on the wire (host, session, unit),
// with no proto or daemon change. Pure: it takes decoded events and returns data, so the view
// builds the tree DOM.

import { Kind, type ActivityEvent } from "@wire/activity/v1alpha1/activity_pb";

// The preview an agent-command event carries when the guard refused the command. The producer
// writes this exact string (cmd/magus guard), alongside "guard: pass", "guard: advise" and
// "observed", so an equality test distinguishes a refusal from the other three.
const DENY_PREVIEW = "guard: deny";

// SessionSpawn is one spawn a session performed: the host tool's action label and the lease the
// handed context declared. unit is empty when the spawn named none, which is the common case: no
// agent host knows a magus lease unless the spawning context passed one down.
export interface SessionSpawn {
  action: string;
  unit: string;
}

// SessionNode is one agent session's slice of the page. commands and denied count AGENT_COMMAND
// events only; leases lists every distinct unit the session's own events carried, first seen
// first. children are the sessions this one spawned, resolved through the lease their commands ran
// under, and each carries its own subtree. events holds the session's events in page order with
// the position each held in the original page, so a view can correlate a leaf with the section
// built from the same index.
export interface SessionNode {
  session: string;
  host: string;
  commands: number;
  denied: number;
  leases: string[];
  spawns: SessionSpawn[];
  children: SessionNode[];
  events: { event: ActivityEvent; index: number }[];
}

// sessionLineage groups the page's agent-command and agent-spawn events into a forest of sessions:
// a session that another session's spawn handed a lease to nests under that spawn's session, and
// every other session is a root. Roots and children alike come back in the order their first event
// appeared in the page.
//
// Events with an empty session are SKIPPED rather than pooled under a placeholder. The field is
// empty when the producer could not attribute the action, so pooling them would assert that
// unrelated unattributed actions came from one agent.
//
// A session is claimed by at most one parent, the first spawn to declare its lease. A claim that
// would make a session its own ancestor is refused, so a trail whose leases cycle still yields a
// finite tree.
export function sessionLineage(events: ActivityEvent[]): SessionNode[] {
  const nodes = new Map<string, SessionNode>();

  events.forEach((event, index) => {
    if (event.kind !== Kind.AGENT_COMMAND && event.kind !== Kind.AGENT_SPAWN) return;
    if (!event.session) return;

    let node = nodes.get(event.session);
    if (!node) {
      node = {
        session: event.session,
        host: event.host,
        commands: 0,
        denied: 0,
        leases: [],
        spawns: [],
        children: [],
        events: [],
      };
      nodes.set(event.session, node);
    }
    // The first event to name a host wins: a later event of the same session that was attributed
    // without one must not blank a host already established.
    if (!node.host) node.host = event.host;
    node.events.push({ event, index });
    if (event.unit && !node.leases.includes(event.unit)) node.leases.push(event.unit);
    if (event.kind === Kind.AGENT_COMMAND) {
      node.commands++;
      if (event.preview === DENY_PREVIEW) node.denied++;
    } else {
      node.spawns.push({ action: event.action, unit: event.unit });
    }
  });

  // Which session ACTED under a lease, versus which session's spawn declared it. A child is the
  // join of the two: the spawn names the lease, and the commands running under that lease say who
  // picked it up.
  const actorOf = new Map<string, string>();
  for (const node of nodes.values()) {
    for (const { event } of node.events) {
      if (event.kind !== Kind.AGENT_COMMAND || !event.unit) continue;
      if (!actorOf.has(event.unit)) actorOf.set(event.unit, node.session);
    }
  }

  const parentOf = new Map<string, string>();
  for (const node of nodes.values()) {
    for (const spawn of node.spawns) {
      if (!spawn.unit) continue;
      const child = actorOf.get(spawn.unit);
      if (!child || child === node.session) continue;
      if (parentOf.has(child)) continue;
      if (descends(parentOf, node.session, child)) continue;
      parentOf.set(child, node.session);
      const childNode = nodes.get(child);
      if (childNode) node.children.push(childNode);
    }
  }

  return [...nodes.values()].filter((node) => !parentOf.has(node.session));
}

// descends reports whether session already sits under ancestor, which is what a claim must not
// create in reverse.
function descends(parentOf: Map<string, string>, session: string, ancestor: string): boolean {
  let at: string | undefined = session;
  const seen = new Set<string>();
  while (at && !seen.has(at)) {
    if (at === ancestor) return true;
    seen.add(at);
    at = parentOf.get(at);
  }
  return false;
}

// sessionLabel is a session branch's text: who ran it, how much it did, and what it broke. The
// counts lead because they are what a reader scans a fan-out for; the leases trail because they
// only matter once a row has drawn attention.
export function sessionLabel(node: SessionNode): string {
  const parts = [node.host || "unknown host"];
  parts.push(node.commands + (node.commands === 1 ? " command" : " commands"));
  if (node.denied > 0) parts.push(node.denied + " denied");
  if (node.leases.length) parts.push(node.leases.join(", "));
  return parts.join(", ");
}
