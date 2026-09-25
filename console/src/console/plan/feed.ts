// The Jobs drawer's live feed: what the selected job's holder is DOING, right now, without
// anybody asking it.
//
// Everything else in this drawer is a declaration or a verdict - what a job was handed, what
// it has released, whether its gates passed. Those answer "is it done". This answers "is it
// going", which is the question somebody actually has at 4pm when a worker has been quiet
// for twenty minutes and interrupting it would cost it the turn it is in.
//
// THREE PRODUCERS, ONE SUBSCRIPTION. The server merges file changes attributed by write
// lane, the guard's tool-call observations attributed by lease, and the runs recorded
// against the job, and it time-orders them before they reach here (see
// magus.activity.v1alpha1.ActivityService.WatchActivityEvents). A client stitching three
// feeds would show a deny after the run it blocked.

import { createClient } from "@connectrpc/connect";
import { ActivityService, Kind, Outcome } from "@wire/activity/v1alpha1/activity_pb";
import type { ActivityEvent } from "@wire/activity/v1alpha1/activity_pb";
import { createServerTransport, getLiveToken } from "../../lib/server";
import { h } from "../view";

// BACKFILL is what the drawer asks for when it opens. A stream that started at "now" would
// paint a blank panel onto a job that has been running for an hour, and a blank panel reads
// as "nothing has happened" rather than as "I only just started listening".
const BACKFILL = 60;

// KEEP bounds the rows held for one job. A worker editing a directory can produce hundreds of
// changes a minute, and a drawer is read by scrolling the last screenful: past this the
// oldest rows are dropped rather than grown into a session-long transcript nobody reads.
const KEEP = 200;

// FeedRow is one rendered line. Flat and pre-formatted, because the render path appends one
// of these per event and a shape it had to interpret would be interpreted on every one.
export interface FeedRow {
  readonly at: number; // unix milliseconds
  readonly kind: "file" | "tool" | "run" | "other";
  readonly label: string; // the path, the tool, or the command
  readonly mark: string; // the guard's verdict, or a run's outcome
  readonly note: string; // contested paths, which gate a run was, why it failed
  readonly bad: boolean; // a deny, an ask, or a failed run: the rows somebody is scanning for
}

// rowOf projects one wire event onto a line. An event whose kind this console does not know
// still renders, as "other" with its action: a feed that silently dropped what it could not
// classify would go quiet exactly when the server grew something new to say.
export function rowOf(e: ActivityEvent): FeedRow {
  const at = e.time ? Number(e.time.seconds) * 1000 + Math.floor(e.time.nanos / 1e6) : 0;
  const failed = e.outcome === Outcome.ERROR;
  switch (e.kind) {
    case Kind.FILE_CHANGE:
      return {
        at,
        kind: "file",
        label: e.action,
        // A contested path is attributed to NOBODY, and both claimants are told. The mark
        // says so rather than leaving the row looking like an ordinary edit, because the
        // fact worth seeing is that the plan has two live lanes over one file.
        mark: e.contested.length ? "contested" : "",
        note: e.contested.length
          ? e.contested.join(" and ") + " both declare this path"
          : e.preview,
        bad: e.contested.length > 0,
      };
    case Kind.RUN:
      return {
        at,
        kind: "run",
        label: e.action,
        mark: failed ? "failed" : "ok",
        note: [e.preview, e.error].filter(Boolean).join(": "),
        bad: failed,
      };
    case Kind.AGENT_COMMAND:
    case Kind.SANDBOX_DENIAL: {
      // The guard writes its verdict into the preview as "guard: <decision>", and "observed"
      // when it judged nothing. Read off the event rather than fetched, so a two-hundred-row
      // feed does not cost two hundred payload reads to say "deny".
      const verdict = e.preview.startsWith("guard: ") ? e.preview.slice(7) : "";
      return {
        at,
        kind: "tool",
        label: e.action,
        mark: verdict,
        note: e.error,
        bad: verdict === "deny" || verdict === "ask" || failed,
      };
    }
    default:
      return { at, kind: "other", label: e.action, mark: "", note: e.preview, bad: failed };
  }
}

// JobFeed holds one job's rows and the element showing them. It owns its own element so the
// detail sheet can be rebuilt around it without the feed losing its place: a stream that
// restarted every time a poll tick repainted the sheet would show the last second of a
// worker's life and nothing else.
export class JobFeed {
  readonly el: HTMLElement;
  private rows: FeedRow[] = [];
  private job = "";
  private abort: AbortController | null = null;
  private trouble = "";

  constructor() {
    this.el = h("div", "console-plan-detail__feed");
    this.render();
  }

  // follow points the feed at a job, or at nothing. Re-pointing it at the job it is already
  // following is a no-op rather than a restart, because the caller is a selection sync that
  // runs on every repaint.
  follow(host: string, job: string | null): void {
    if (job === (this.job || null)) return;
    this.stop();
    this.job = job ?? "";
    this.rows = [];
    this.trouble = "";
    this.render();
    if (!job || !host) return;
    this.abort = new AbortController();
    void this.stream(host, job, this.abort.signal);
  }

  // stop ends the subscription. Called when the selection moves and when the surface is
  // unmounted: a stream nobody is reading is a stream the server is still writing to.
  stop(): void {
    this.abort?.abort();
    this.abort = null;
  }

  private async stream(host: string, job: string, signal: AbortSignal): Promise<void> {
    try {
      const client = createClient(ActivityService, createServerTransport(host, getLiveToken()));
      const stream = client.watchActivityEvents(
        { backfill: BACKFILL, filter: { units: [job] } },
        { signal },
      );
      for await (const e of stream) {
        if (this.job !== job) return; // the selection moved while this was in flight
        this.push(rowOf(e));
      }
      // The stream ended without an error: the server closed it, or the page is going away.
      // Said plainly rather than left looking live, because a feed that stopped and still
      // looks like a feed is how a person concludes a worker went quiet.
      if (this.job === job) {
        this.trouble = "The feed ended. Reopen this job to start it again.";
        this.render();
      }
    } catch (err) {
      if (signal.aborted || this.job !== job) return;
      this.trouble = "The feed could not be read (" + why(err) + ").";
      this.render();
    }
  }

  private push(row: FeedRow): void {
    this.rows.push(row);
    if (this.rows.length > KEEP) this.rows = this.rows.slice(-KEEP);
    this.render();
  }

  private render(): void {
    const box = h("div", "console-plan-feed");
    const head = h("h3", "console-plan-detail__runshead", "Live");
    box.append(head);
    if (!this.job) {
      box.append(h("p", "console-plan-detail__hint", "Select a job to watch what it is doing."));
    } else if (this.trouble) {
      box.append(h("p", "console-plan-detail__hint", this.trouble));
    } else if (!this.rows.length) {
      box.append(
        h(
          "p",
          "console-plan-detail__hint",
          "Nothing yet. Files changed under this job's write paths, the tool calls the guard" +
            " saw under its lease, and its runs all appear here as they happen.",
        ),
      );
    } else {
      const ul = h("ul", "console-plan-feed__list");
      ul.setAttribute("role", "list");
      // Newest last, and the container scrolls: this reads like a terminal, which is what a
      // person watching work happen already knows how to read.
      for (const row of this.rows) ul.append(lineOf(row));
      box.append(ul);
    }
    this.el.replaceChildren(box);
    // Only when there is something to scroll to. Calling this on an empty list moves nothing
    // and still fights a reader who has scrolled up to read an older deny.
    const list = this.el.querySelector(".console-plan-feed__list");
    if (list) list.scrollTop = list.scrollHeight;
  }
}

function lineOf(row: FeedRow): HTMLElement {
  const li = h("li", "console-plan-feed__row");
  li.dataset.kind = row.kind;
  if (row.bad) li.dataset.bad = "";
  li.append(h("span", "console-plan-feed__time", clock(row.at)));
  li.append(h("span", "console-plan-feed__kind", row.kind));
  if (row.mark) li.append(h("span", "console-plan-feed__mark", row.mark));
  li.append(h("code", "console-plan-feed__label", row.label));
  if (row.note) li.append(h("span", "console-plan-feed__note", row.note));
  return li;
}

// clock is the wall time, not an age. An age on every row would have to tick, and a feed
// that rewrote every line once a second would fight the reader's scroll position.
function clock(ms: number): string {
  if (!ms) return "";
  return new Date(ms).toLocaleTimeString([], { hour12: false });
}

function why(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}
