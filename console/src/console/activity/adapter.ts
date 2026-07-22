// adapter.ts - maps the daemon's activity trail (magus.activity.v1 ActivityEvent list) into
// the shared RenderModel (console/render/model), so the activity view paints each recorded
// action with the SAME foldable, status-accented section renderer the log viewer uses. One
// event becomes one section: a one-line head (action, actor, outcome, duration, time) over a
// body of details (error text, payload sizes and refs, response preview, workspace). Pure: no
// DOM, no network - it takes the decoded events and returns the model, so it is unit-testable.

import type { Timestamp } from "@bufbuild/protobuf/wkt";
import type { Duration } from "@bufbuild/protobuf/wkt";
import { Kind, Outcome, type ActivityEvent } from "../../gen/magus/activity/v1/activity_pb";
import type { RenderModel, Section } from "../render/model";

// kindLabel is the short source tag shown in the head (the enum's stable, terse name).
export function kindLabel(kind: Kind): string {
  switch (kind) {
    case Kind.MCP_TOOL_CALL:
      return "mcp";
    case Kind.JOB:
      return "job";
    case Kind.CONFIG_CHANGE:
      return "config";
    case Kind.TOKEN_LIFECYCLE:
      return "token";
    case Kind.SANDBOX_DENIAL:
      return "sandbox";
    default:
      return "event";
  }
}

// tsMillis converts a protobuf Timestamp to epoch milliseconds, or null when absent.
export function tsMillis(t: Timestamp | undefined): number | null {
  if (!t) return null;
  return Number(t.seconds) * 1000 + Math.floor(t.nanos / 1e6);
}

// clockTime renders an epoch-ms instant as a local wall-clock HH:MM:SS, the trail's identity
// column. A trail is scanned by "when did this happen", so the absolute time leads.
export function clockTime(ms: number | null): string {
  if (ms === null) return "";
  const d = new Date(ms);
  const p = (n: number): string => String(n).padStart(2, "0");
  return p(d.getHours()) + ":" + p(d.getMinutes()) + ":" + p(d.getSeconds());
}

// durText renders a protobuf Duration as "12ms" / "1.2s"; "" when absent or zero.
export function durText(d: Duration | undefined): string {
  if (!d) return "";
  const ms = Number(d.seconds || 0n) * 1000 + Number(d.nanos || 0) / 1e6;
  if (ms <= 0) return "";
  return ms < 1000 ? Math.round(ms) + "ms" : (ms / 1000).toFixed(1) + "s";
}

// humanBytes renders a byte count as "512 B" / "1.4 KB" / "2.0 MB".
export function humanBytes(n: number): string {
  if (n < 1024) return n + " B";
  if (n < 1024 * 1024) return (n / 1024).toFixed(1) + " KB";
  return (n / 1024 / 1024).toFixed(1) + " MB";
}

// eventSection maps one ActivityEvent to a Section. meta.status is the accent stem the
// renderer keys the left-rule on: an errored action is "fail" (red), an ok action "pass"
// (green). No leading "[token]" is put in the title, so the head carries no misleading badge -
// the accent alone marks the outcome. meta.label is the kind, mirroring the log viewer's meta.
export function eventSection(ev: ActivityEvent): Section {
  const ok = ev.outcome !== Outcome.ERROR;
  const status = ok ? "pass" : "fail";
  const kind = kindLabel(ev.kind);
  const when = clockTime(tsMillis(ev.time));
  const dur = durText(ev.duration);

  // Head: the action and actor lead; the terse meta (kind, outcome, duration, time) trails
  // after a wide gap so the row scans left-to-right as "what - who ... how it went, when".
  const lead = [ev.action || kind, ev.actor].filter(Boolean).join("  ");
  const meta = [kind, ok ? "ok" : "error", dur, when].filter(Boolean).join(" - ");
  const title = meta ? lead + "   " + meta : lead;

  const body: string[] = [];
  if (ev.error) body.push(ev.error);
  const sizes: string[] = [];
  if (ev.requestBytes > 0n)
    sizes.push(
      "request " +
        humanBytes(Number(ev.requestBytes)) +
        (ev.requestRef ? "  " + ev.requestRef : ""),
    );
  if (ev.responseBytes > 0n)
    sizes.push(
      "response " +
        humanBytes(Number(ev.responseBytes)) +
        (ev.responseRef ? "  " + ev.responseRef : ""),
    );
  if (sizes.length) body.push(sizes.join("   "));
  if (ev.preview) for (const line of ev.preview.split("\n")) body.push(line);
  if (ev.workspace) body.push("workspace: " + ev.workspace);

  return { title, lines: [title, ...body], meta: { label: kind, status } };
}

// activityToModel maps a page of events (newest first, as the service returns them) into the
// RenderModel the shared section renderer consumes. Every event is a titled section.
export function activityToModel(events: ActivityEvent[]): RenderModel {
  const sections: Section[] = events.map(eventSection);
  return { sections, titled: sections.length };
}

// KindGroup is one bucket of the activity index tree: a kind, its human heading, and the events of
// that kind (newest first, the order the service returns them). index is the event's position in the
// original page, so the view can correlate a tree leaf with its rendered section.
export interface KindGroup {
  kind: Kind;
  label: string;
  events: { event: ActivityEvent; index: number }[];
}

// The fixed display order + headings for the activity index tree, so it always lists sources in the
// same order regardless of what a given page happens to contain.
const KIND_GROUP_ORDER: ReadonlyArray<{ kind: Kind; label: string }> = [
  { kind: Kind.MCP_TOOL_CALL, label: "MCP tool calls" },
  { kind: Kind.JOB, label: "Jobs" },
  { kind: Kind.CONFIG_CHANGE, label: "Config changes" },
  { kind: Kind.TOKEN_LIFECYCLE, label: "Token lifecycle" },
  { kind: Kind.SANDBOX_DENIAL, label: "Sandbox denials" },
];

// groupEventsByKind buckets a page of events into the fixed kind order, dropping empty groups so the
// index lists only kinds that actually occurred. Each retained event keeps its original page index.
// Any unrecognized kind collects under a trailing "Other" so nothing is silently dropped. Pure: it
// returns data; the view builds the tree DOM.
export function groupEventsByKind(events: ActivityEvent[]): KindGroup[] {
  const buckets = new Map<Kind, { event: ActivityEvent; index: number }[]>();
  events.forEach((event, index) => {
    const list = buckets.get(event.kind) ?? [];
    if (list.length === 0) buckets.set(event.kind, list);
    list.push({ event, index });
  });

  const groups: KindGroup[] = [];
  const known = new Set<Kind>();
  for (const { kind, label } of KIND_GROUP_ORDER) {
    known.add(kind);
    const list = buckets.get(kind);
    if (list && list.length) groups.push({ kind, label, events: list });
  }
  const rest: { event: ActivityEvent; index: number }[] = [];
  for (const [kind, list] of buckets) if (!known.has(kind)) rest.push(...list);
  if (rest.length) groups.push({ kind: Kind.UNSPECIFIED, label: "Other", events: rest });
  return groups;
}
