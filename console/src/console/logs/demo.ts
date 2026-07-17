// demo.ts - the daemon-free showcase (#demo). Synthesize a realistic single-invocation Journal
// (a `magus affected ci` run over ~6 targets, one of which fails), then REVEAL its events
// incrementally so the page feels like a live run streaming in. It reuses the live-stream buffer
// (state.liveEvents / liveInvocation) and scheduleLiveRender end to end - the synthetic events are
// the SAME magus.viewer.v1 Event messages the wire carries (built with create(EventSchema, ...)),
// so buildModelFromEvents (pretty view), buildSpans, renderWaterfall, waterfallSource, and
// updateTimelineControl all run exactly as they would for a real journal. No new render path,
// no transport, nothing fetched. It is an easter egg themed as a Linux kernel build.

import { create } from "@bufbuild/protobuf";
import type { Journal } from "../../gen/magus/viewer/v1/viewer_pb";
import { EventSchema, JournalSchema, Kind, Status, Stream, Trigger } from "../../gen/magus/viewer/v1/viewer_pb";
import { state } from "./state";
import { emptyEl, setRefIdentity } from "./dom";
import { tsMs } from "./waterfall";
import { scheduleLiveRender, setLiveStatus } from "./live";

// One planned target in the synthetic run: its (project, target), the kbuild step commands, the
// outcome, and its start offset + duration (ms) so the windows overlap into a real cascade.
interface DemoTarget {
  project: string;
  target: string;
  execs: string[];
  status: Status;
  start: number;
  dur: number;
}

// demoTs / demoDur build protobuf Timestamp / Duration inits ({seconds: bigint, nanos}) from a
// millisecond value, the shapes tsMs / durMs decode.
function demoTs(ms: number): { seconds: bigint; nanos: number } {
  return { seconds: BigInt(Math.floor(ms / 1000)), nanos: Math.floor((ms % 1000) * 1e6) };
}
function demoDur(ms: number): { seconds: bigint; nanos: number } {
  return { seconds: BigInt(Math.floor(ms / 1000)), nanos: Math.floor((ms % 1000) * 1e6) };
}

// demoOutputFor returns a plausible stdout line for a synthesized EXEC command. The demo is
// an easter egg themed as a Linux kernel build, so these echo authentic kbuild chatter.
function demoOutputFor(cmd: string): string {
  if (cmd.startsWith("CC ")) return "  " + cmd;
  if (cmd.startsWith("AR ")) return "  " + cmd;
  if (cmd.startsWith("LD ")) return "  " + cmd;
  if (cmd.startsWith("OBJCOPY")) return "  " + cmd;
  if (cmd.startsWith("MODPOST")) return "  " + cmd;
  if (cmd.startsWith("make")) return "  SYNC   include/config/auto.conf";
  return "  " + cmd;
}

// synthDemoJournal builds the showcase Journal: a STARTED/SCOPE preamble, then per target a
// set of EXEC step markers (each with an output line) staggered across the target's window and
// a terminal RESULT (status + duration + a synthetic ref), then FINISHED. Start offsets and
// durations overlap so the waterfall renders a real cascade; the Invocation start/end frame the
// axis. drivers/net fails with a modpost undefined symbol for the red span; arch/x86 is a fast
// incremental (cached) hit. Everything is synthesized log TEXT only - no kernel source is copied.
function synthDemoJournal(): Journal {
  const base = Date.now();
  // [project, target, exec command lines (kbuild steps), status, start offset ms, duration ms]
  const plan: DemoTarget[] = [
    { project: "arch/x86", target: "vmlinux", execs: ["SYNC .config", "CC arch/x86/kernel/cpu/common.o"], status: Status.CACHED, start: 0, dur: 60 },
    { project: "kernel", target: "built-in", execs: ["CC kernel/sched/core.o", "CC kernel/fork.o", "AR kernel/built-in.a"], status: Status.PASS, start: 200, dur: 2600 },
    { project: "mm", target: "built-in", execs: ["CC mm/page_alloc.o", "CC mm/slub.o", "AR mm/built-in.a"], status: Status.PASS, start: 350, dur: 2200 },
    { project: "fs", target: "built-in", execs: ["CC fs/namei.o", "CC fs/ext4/inode.o", "AR fs/built-in.a"], status: Status.PASS, start: 600, dur: 3000 },
    { project: "drivers/net", target: "built-in", execs: ["CC drivers/net/ethernet/intel/e1000/e1000_main.o", "MODPOST modules-only.symvers"], status: Status.FAIL, start: 1100, dur: 2400 },
    { project: ".", target: "vmlinux", execs: ["LD vmlinux", "OBJCOPY arch/x86/boot/bzImage"], status: Status.PASS, start: 3700, dur: 1600 },
  ];
  const command = { verb: "run", args: ["vmlinux", "--", "make", "-j$(nproc)"], cwd: "/usr/src/linux", trigger: Trigger.RUN };
  const events = [];
  events.push(create(EventSchema, { kind: Kind.STARTED, time: demoTs(base), command, magusVersion: "demo" }));
  events.push(create(EventSchema, { kind: Kind.SCOPE, time: demoTs(base), text: "projects: arch/x86, kernel, mm, fs, drivers/net" }));

  let maxEnd = 0;
  let n = 0;
  for (const p of plan) {
    const start = base + p.start;
    for (let i = 0; i < p.execs.length; i++) {
      const at = start + Math.round((p.dur * i) / p.execs.length);
      events.push(create(EventSchema, { kind: Kind.EXEC, time: demoTs(at), project: p.project, target: p.target, text: p.execs[i] }));
      const out = demoOutputFor(p.execs[i]);
      if (out) events.push(create(EventSchema, { kind: Kind.OUTPUT, time: demoTs(at + 12), project: p.project, target: p.target, stream: Stream.STDOUT, text: out }));
    }
    const end = start + p.dur;
    maxEnd = Math.max(maxEnd, end - base);
    if (p.status === Status.FAIL) {
      events.push(create(EventSchema, { kind: Kind.OUTPUT, time: demoTs(end - 8), project: p.project, target: p.target, stream: Stream.STDERR, text: "ERROR: modpost: \"e1000_probe\" [drivers/net/ethernet/intel/e1000/e1000.ko] undefined!" }));
    }
    if (p.status === Status.PASS && p.project === "." && p.target === "vmlinux") {
      events.push(create(EventSchema, { kind: Kind.OUTPUT, time: demoTs(end - 8), project: p.project, target: p.target, stream: Stream.STDOUT, text: "Kernel: arch/x86/boot/bzImage is ready  (#1)" }));
    }
    const ref = "outd" + (n++).toString(16).padStart(6, "0");
    events.push(create(EventSchema, { kind: Kind.RESULT, time: demoTs(end), project: p.project, target: p.target, status: p.status, ref, duration: demoDur(p.dur) }));
  }
  events.push(create(EventSchema, { kind: Kind.FINISHED, time: demoTs(base + maxEnd), level: "error" }));
  // Reveal in time order so the pretty view and waterfall both cascade as events arrive.
  events.sort((a, b) => (tsMs(a.time) || 0) - (tsMs(b.time) || 0));

  const invocation = { id: "invdemo01", command, startTime: demoTs(base), endTime: demoTs(base + maxEnd), magusVersion: "demo" };
  return create(JournalSchema, { invocation, events });
}

// startDemo enters the showcase: it frames the axis from the synthetic Invocation, opens the
// waterfall, and streams the events in over a few seconds via the shared live buffer.
export function startDemo(): void {
  const journal = synthDemoJournal();
  const ordered = journal.events;
  // A SECOND, already-finished invocation shown alongside the streaming one, so the demo also
  // showcases multi-invocation (two groups on a shared axis). Relabelled so its header differs.
  const j2 = synthDemoJournal();
  if (j2.invocation && j2.invocation.command) { j2.invocation.command.verb = "affected"; j2.invocation.command.args = ["ci"]; }

  stopDemo();
  state.demoActive = true;
  state.liveEvents = [];
  state.liveInvocation = journal.invocation ?? null; // frames the axis (start/end) and the command preamble
  state.livePaused = false;
  state.currentJournal = null; // waterfallSource then reads the live buffer, as in a real live run
  state.currentJournals = [j2]; // the completed sibling invocation, rendered as its own group
  state.timeline = true;       // open straight into the waterfall so it visibly fills in
  state.currentRef = "";
  if (emptyEl) emptyEl.hidden = true;
  // No real identity in demo mode - an empty value hides the ref pill entirely (setRefIdentity)
  // rather than showing a bordered box around the literal word "demo".
  setRefIdentity("", false);
  setLiveStatus("streaming");

  // Prime the first frame with enough events that a target span exists immediately (so the
  // waterfall is never momentarily empty): the STARTED/SCOPE preamble plus the first EXEC.
  let i = 0;
  const prime = Math.min(3, ordered.length);
  for (; i < prime; i++) state.liveEvents.push(ordered[i]);
  scheduleLiveRender();

  const BATCH = 3;
  const TICK_MS = 360;
  state.demoTimer = window.setInterval(() => {
    if (i >= ordered.length) {
      // Everything is already revealed and rendered by the prior batch tick; just settle the
      // status pill. (Re-rendering here would race scheduleLiveRender's rAF back to "streaming".)
      stopDemo();
      setLiveStatus("done");
      return;
    }
    for (let k = 0; k < BATCH && i < ordered.length; k++) state.liveEvents.push(ordered[i++]);
    scheduleLiveRender();
  }, TICK_MS);

  // Clear the reveal if the page is being torn down, so no timer fires after navigation.
  window.addEventListener("pagehide", stopDemo, { once: true });
}

export function stopDemo(): void {
  if (state.demoTimer !== null) {
    window.clearInterval(state.demoTimer);
    state.demoTimer = null;
  }
  state.demoActive = false;
}
