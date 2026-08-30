// jobs.ts - the Activity surface's maintenance control: every job the daemon registers
// (JobService.ListJobs), its running state, its last run, the current size of what it maintains, and
// a Run action per job (JobService.RunJob).
//
// WHY this earns a place beside `magus server job <name>`, which already runs all of them: the
// prompt to run one FIRES INSIDE THE CONSOLE. watch.ts's daemon-storage watcher reads the cache
// figure off the live status snapshot and rings a warn when it crosses its threshold - a notice
// whose whole remedy is a job. With no control here, the surface that notices the problem cannot act
// on it, and the reader pays a terminal round-trip to carry out a decision the console has already
// made. The CLI stays the other door: JobService is the same submit path `server job` takes.
//
// It is deliberately list-and-run and nothing else, because that is the whole contract: two RPCs, a
// fixed registry, no schedule and no cancel. A control that offered more would be describing a
// service that does not exist.
//
// The control lives on Activity rather than the dashboard because a job's RESULT is a trail entry:
// what it did is read on this surface, so what starts it belongs here too.

import { createClient, type Client } from "@connectrpc/connect";
import { JobService, SubmitState, type Job } from "@wire/job/v1alpha1/job_pb";
import { createDaemonTransport, getLiveToken } from "../../lib/daemon";
import { errMessage } from "../../lib/guards";
import { humanBytes } from "./adapter";
import { relTime } from "../logs/runtree";
import { h } from "../view";

// JobsControl is one mounted control: the element to place, a load() that (re)reads the registry,
// and destroy() to drop late responses, the same stale-guard shape the surface uses for its own
// loads.
export interface JobsControl {
  el: HTMLElement;
  load(): Promise<void>;
  destroy(): void;
}

// jobId is the bare id in a "jobs/{job}" resource name - what the CLI's `server job <name>` takes,
// and the only spelling worth showing a reader.
function jobId(name: string): string {
  return name.startsWith("jobs/") ? name.slice("jobs/".length) : name;
}

// meta is the one-line "how much there is, and when it was last dealt with" for a job. Both halves
// are optional on the wire: sync-graph reconciles rather than trims so it reports no size, and a job
// that has not run this daemon session carries no last run.
function meta(job: Job, now: number): string {
  const parts: string[] = [];
  const size = job.target ? Number(job.target.sizeBytes) : 0;
  const count = job.target ? Number(job.target.itemCount) : 0;
  if (size > 0) parts.push(humanBytes(size));
  if (count > 0) parts.push(count + (count === 1 ? " item" : " items"));
  const last = job.lastRun;
  if (last?.endTime) {
    const when = relTime(Number(last.endTime.seconds) * 1000, now);
    parts.push("last run " + when + (last.ok ? "" : " (failed)"));
  }
  return parts.join(" - ");
}

// mountJobs builds the control against daemonHost. It starts hidden and shows itself once ListJobs
// answers: a daemon too old to serve the job service, or one that refuses it, leaves the surface
// exactly as it was rather than parking an empty card above the trail.
export function mountJobs(daemonHost: string): JobsControl {
  const client: Client<typeof JobService> = createClient(
    JobService,
    createDaemonTransport(daemonHost, getLiveToken()),
  );
  let stale = false;

  const el = h("section", "pf-v6-c-card console-activity-jobs");
  el.hidden = true;
  const head = h("div", "pf-v6-c-card__title console-activity-jobs__head");
  head.append(h("span", undefined, "Maintenance"));
  const note = h("span", "console-activity-jobs__note");
  head.append(note);
  const body = h("div", "pf-v6-c-card__body");
  el.append(head, body);

  // run submits one job. ALREADY_RUNNING is a SUCCESS state on this contract (the daemon coalesced
  // an identical in-flight job), so it reads as a fact on the row rather than an error; only a real
  // refusal - an unknown name, no socket to submit to, a rejected token - lands in the catch, and
  // that one re-enables the button, because the reader can retry it and a dead button says nothing.
  //
  // The list is deliberately NOT re-read here. A submit is fire-and-forget, so a re-list this soon
  // races the worker it just started, and the repaint would wipe the outcome the reader asked for.
  // The event index's refresh control reloads the surface, this control included.
  async function run(job: Job, btn: HTMLButtonElement, state: HTMLElement): Promise<void> {
    if (btn.disabled) return;
    btn.disabled = true;
    state.textContent = "starting...";
    try {
      const resp = await client.runJob({ name: job.name });
      if (stale) return;
      state.textContent =
        resp.state === SubmitState.ALREADY_RUNNING ? "already running" : "started";
    } catch (e) {
      if (stale) return;
      state.textContent = "could not run " + jobId(job.name) + ": " + errMessage(e);
      btn.disabled = false;
    }
  }

  function row(job: Job, now: number): HTMLElement {
    const rowEl = h("div", "console-activity-jobs__row");
    rowEl.append(h("span", "console-activity-jobs__name", jobId(job.name)));
    rowEl.append(h("span", "console-activity-jobs__desc", job.description));
    rowEl.append(h("span", "console-activity-jobs__meta", meta(job, now)));
    const btn = h("button", "pf-v6-c-button pf-m-secondary pf-m-small") as HTMLButtonElement;
    btn.type = "button";
    btn.append(h("span", "pf-v6-c-button__text", "Run"));
    btn.setAttribute("aria-label", "Run " + jobId(job.name));
    const state = h("span", "console-activity-jobs__state");
    // Running is transient state, so it rides a data-* attribute rather than a --modifier class
    // (console/README.md). The button stays live: a submit against a running job coalesces.
    if (job.running) {
      rowEl.dataset.state = "running";
      state.textContent = "running";
    }
    btn.addEventListener("click", () => void run(job, btn, state));
    rowEl.append(btn, state);
    return rowEl;
  }

  async function load(): Promise<void> {
    try {
      const resp = await client.listJobs({});
      if (stale) return;
      const now = Date.now();
      note.textContent = "";
      body.replaceChildren(...resp.jobs.map((job) => row(job, now)));
      el.hidden = resp.jobs.length === 0;
    } catch (e) {
      if (stale) return;
      // Reached only after the trail itself loaded, so the daemon is up and this is the job service
      // specifically saying no. Saying which service failed beats a card that is silently absent.
      note.textContent = "could not list jobs: " + errMessage(e);
      body.replaceChildren();
      el.hidden = false;
    }
  }

  return {
    el,
    load,
    destroy(): void {
      stale = true;
      el.remove();
    },
  };
}
