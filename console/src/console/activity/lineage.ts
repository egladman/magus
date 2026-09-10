// lineage.ts - groups a page of activity events by the agent SESSION that produced them, and joins
// a spawning session to the sessions its spawn handed a lease to. The trail records one flat
// time-ordered list, so the fan-out an orchestrator produced reads as interleaved rows from
// unrelated agents; this recovers the shape from fields already on the wire (host, session, unit),
// with no proto or daemon change. Pure: it takes decoded events and returns data, so the view
// builds the tree DOM.

import { Kind, type ActivityEvent } from "@wire/activity/v1alpha1/activity_pb";
import { guardDecision } from "../dashboard/state";
import { tsMillis } from "./adapter";

// SessionSpawn is one spawn a session performed: the host tool's action label and the lease the
// handed context declared. lease is empty when the spawn named none, which is the common case: no
// agent host knows a magus lease unless the spawning context passed one down.
export interface SessionSpawn {
  action: string;
  lease: string;
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

// sessionKey identifies a session across hosts. Session ids are minted by the host tool, so two
// hosts can hand out the same id for unrelated sessions; the host is part of the identity.
export function sessionKey(host: string, session: string): string {
  return host + "\0" + session;
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
// A session is claimed by at most one parent, the earliest spawn (by event time) to declare a
// lease it acted under. A claim that would make a session its own ancestor is refused, so a trail
// whose leases cycle still yields a finite tree. The page's own order does not decide any of this:
// a page is newest-first, and the join reads the same whichever way it is listed.
export function sessionLineage(events: ActivityEvent[]): SessionNode[] {
  const nodes = new Map<string, SessionNode>();
  const spawns: { key: string; lease: string; at: number; index: number }[] = [];

  events.forEach((event, index) => {
    if (event.kind !== Kind.AGENT_COMMAND && event.kind !== Kind.AGENT_SPAWN) return;
    if (!event.session) return;

    const key = sessionKey(event.host, event.session);
    let node = nodes.get(key);
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
      nodes.set(key, node);
    }
    node.events.push({ event, index });
    if (event.unit && !node.leases.includes(event.unit)) node.leases.push(event.unit);
    if (event.kind === Kind.AGENT_COMMAND) {
      node.commands++;
      if (guardDecision(event.preview) === "deny") node.denied++;
    } else {
      node.spawns.push({ action: event.action, lease: event.unit });
      if (event.unit) {
        spawns.push({ key, lease: event.unit, at: tsMillis(event.time) ?? 0, index });
      }
    }
  });

  // Which sessions ACTED under a lease, versus which session's spawn declared it. A child is the
  // join of the two: the spawn names the lease, and the commands running under that lease say who
  // picked it up. Every actor is kept: a lease re-bound to a second worker has two.
  const actorsOf = new Map<string, Set<string>>();
  for (const [key, node] of nodes) {
    for (const { event } of node.events) {
      if (event.kind !== Kind.AGENT_COMMAND || !event.unit) continue;
      let actors = actorsOf.get(event.unit);
      if (!actors) {
        actors = new Set<string>();
        actorsOf.set(event.unit, actors);
      }
      actors.add(key);
    }
  }

  // Oldest spawn first. An event with no time sorts by page position, which is newest-first, so a
  // later index is the older event.
  spawns.sort((a, b) => a.at - b.at || b.index - a.index);
  const parentOf = new Map<string, string>();
  for (const spawn of spawns) {
    // The spawner binding itself to the lease it registered is not a child of itself.
    for (const child of actorsOf.get(spawn.lease) ?? []) {
      if (child === spawn.key) continue;
      if (parentOf.has(child)) continue;
      if (descends(parentOf, spawn.key, child)) continue;
      parentOf.set(child, spawn.key);
    }
  }

  for (const [key, node] of nodes) {
    const parent = parentOf.get(key);
    if (parent) nodes.get(parent)?.children.push(node);
  }
  return [...nodes.entries()].filter(([key]) => !parentOf.has(key)).map(([, node]) => node);
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
