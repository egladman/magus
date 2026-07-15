// model.ts - builds the {sections,...} render model the pretty/raw views consume, from either
// a heuristic text parse (a pasted/dropped/#src log has no structure) or a magus.viewer.v1
// event stream (the #data / live / demo paths, where grouping and status are exact, not
// regex-guessed). Both produce the SAME model shape so render()/search/fold/copy work unchanged.
// Pure: no DOM, no shared-state mutation - it takes its inputs as arguments.

import { Kind, Status } from "../../gen/magus/viewer/v1/viewer_pb";
import type { Command } from "../../gen/magus/viewer/v1/viewer_pb";
import type { Duration } from "@bufbuild/protobuf/wkt";
import type { BuiltModel, InvocationView, RenderModel, Section, Source } from "./state";
import { stripAnsi } from "../render/ansi";

// --- Model: split the text into foldable sections -----------------------------
// A section begins at a recognized header line (a run header, a target status line,
// or a "-- project (failed) --" divider). Lines before the first header form an
// untitled preamble section that renders without a fold head.
export function isHeaderLine(line: string): boolean {
  const s = stripAnsi(line);
  return (
    /^-- .+ --\s*$/.test(s) ||
    /^\[(pass|fail|warn|dry|error|info)\]/.test(s) ||
    /^(projects|charms):/.test(s)
  );
}

export function buildModel(text: string): RenderModel {
  const lines = text.replace(/\r\n?/g, "\n").split("\n");
  // Drop a single trailing empty line from the split so a log ending in "\n" does
  // not render a blank final row.
  if (lines.length && lines[lines.length - 1] === "") lines.pop();

  const sections: Section[] = [];
  let current: Section = { title: null, lines: [] };
  for (const line of lines) {
    if (isHeaderLine(line)) {
      if (current.title !== null || current.lines.length) sections.push(current);
      current = { title: line, lines: [line] };
    } else {
      current.lines.push(line);
    }
  }
  if (current.title !== null || current.lines.length) sections.push(current);
  const titled = sections.filter((s) => s.title !== null).length;
  return { sections, titled };
}

// A working (project, target) group accumulated while scanning an event stream.
interface EventGroup {
  project: string;
  target: string;
  body: string[];
  result: { status: Status; duration?: Duration; ref: string } | null;
}

// buildModelFromEvents turns an event stream (a whole Journal's events, or the live events
// so far) into the {sections,...} render model: the invocation command becomes a lineage
// preamble; each (project,target) becomes a section whose exec events open "$ cmd" groups,
// output events are body lines, and the result event sets the status badge/accent in a
// synthesized head line the existing renderer understands. Reused by the static and live
// paths, so it must be a pure function of the events seen so far.
export function buildModelFromEvents(events: Source["events"], invocation: InvocationView): BuiltModel {
  const groups = new Map<string, EventGroup>();
  const order: EventGroup[] = [];
  const preamble: string[] = [];
  const rawLines: string[] = [];
  const cmd = invocation && invocation.command;
  if (cmd && cmd.verb) {
    preamble.push("$ magus " + cmd.verb + (cmd.args && cmd.args.length ? " " + cmd.args.join(" ") : ""));
  }
  for (const ev of events) {
    if (ev.kind === Kind.OUTPUT || ev.kind === Kind.EXEC || ev.kind === Kind.RESULT) {
      const key = ev.project + " " + ev.target;
      let g = groups.get(key);
      if (!g) { g = { project: ev.project, target: ev.target, body: [], result: null }; groups.set(key, g); order.push(g); }
      if (ev.kind === Kind.EXEC) g.body.push("$ " + ev.text);
      else if (ev.kind === Kind.OUTPUT) { g.body.push(ev.text); rawLines.push(ev.text); }
      else g.result = { status: ev.status, duration: ev.duration, ref: ev.ref };
    } else if (ev.kind === Kind.WARN) {
      preamble.push("[warn] " + ev.text);
    } else if (ev.kind === Kind.SCOPE && ev.text) {
      preamble.push(ev.text);
    }
  }
  const sections: Section[] = [];
  if (preamble.length) sections.push({ title: null, lines: preamble });
  for (const g of order) {
    const title = groupTitle(g);
    // meta carries the structured (label, status) the filter matches target:/status: against,
    // so it need not re-parse them out of the rendered title. "" status (no result yet) reads
    // as "running". Preamble sections have no meta and so never match a target:/status: term.
    const label = (g.project && g.project !== "." ? g.project + ":" : "") + (g.target || "output");
    sections.push({ title, lines: [title, ...g.body], meta: { label, status: statusName(g.result ? g.result.status : Status.UNSPECIFIED) || "running" } });
  }
  const titled = sections.filter((s) => s.title !== null).length;
  const summary =
    order.length + (order.length === 1 ? " target, " : " targets, ") +
    rawLines.length + (rawLines.length === 1 ? " line" : " lines");
  return { sections, titled, rawLines, summary };
}

function groupTitle(g: EventGroup): string {
  const st = statusName(g.result ? g.result.status : Status.UNSPECIFIED);
  const name = (g.project && g.project !== "." ? g.project + ":" : "") + (g.target || "output");
  const dur = g.result && g.result.duration ? " (" + durText(g.result.duration) + ")" : "";
  const refTag = g.result && g.result.ref ? "  " + g.result.ref : "";
  return (st ? "[" + st + "] " : "") + name + dur + refTag;
}

export function statusName(s: Status): string {
  if (s === Status.PASS) return "pass";
  if (s === Status.FAIL) return "fail";
  if (s === Status.CACHED) return "cached";
  return "";
}

// durText renders a protobuf Duration ({seconds: bigint, nanos: number}) as "12ms" / "1.2s".
function durText(d: Duration): string {
  const ms = Number(d.seconds || 0n) * 1000 + Number(d.nanos || 0) / 1e6;
  return ms < 1000 ? Math.round(ms) + "ms" : (ms / 1000).toFixed(1) + "s";
}

// cmdLabel renders an invocation's command for a group header, e.g. "magus run vmlinux".
export function cmdLabel(command: Command | null | undefined): string {
  if (!command) return "invocation";
  const args = (command.args || []).join(" ");
  return "magus " + (command.verb || "run") + (args ? " " + args : "");
}

// buildModelMulti builds the pretty-view model across all invocation sources: a single
// invocation renders as before; several are concatenated, each preceded by a command divider.
export function buildModelMulti(sources: Source[]): BuiltModel {
  if (sources.length <= 1) {
    const s = sources[0] || { events: [], invocation: null };
    return buildModelFromEvents(s.events || [], s.invocation);
  }
  const sections: Section[] = [];
  const rawLines: string[] = [];
  let titled = 0;
  for (const s of sources) {
    const b = buildModelFromEvents(s.events || [], s.invocation);
    if (!b.sections.length) continue;
    sections.push({ title: null, lines: ["", cmdLabel(s.invocation && s.invocation.command)] });
    for (const sec of b.sections) sections.push(sec);
    for (const l of b.rawLines) rawLines.push(l);
    titled += b.titled;
  }
  return { sections, titled, rawLines, summary: sources.length + " invocations" };
}
