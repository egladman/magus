// main.ts - the console's Pulls app: the live board of open pull requests.
//
// The server owns the forge cadence. This app re-reads its last board every
// 10s, which is local and cheap, and says how old that board is.

import { clone, toJsonString } from "@bufbuild/protobuf";
import { createClient } from "@connectrpc/connect";
import { BoardSchema, PullsService, type Board } from "@wire/pulls/v1alpha1/pulls_pb";
import { MagusError, fail, fromPromise, ok, type Result } from "../../gen/magus/types";
import {
  adoptServerOrigin,
  consumeLiveToken,
  createServerTransport,
  parseHash,
  resolveServerHostOrRemembered,
  wantsDemo,
} from "../../lib/server";
import { h } from "../../desktop/view";
import type { AppInstance } from "../../desktop/standalone";
import { demoBoard } from "./demo";
import {
  authorsLine,
  renderChips,
  renderMain,
  renderOrder,
  renderPatterns,
  renderRow,
  syncLine,
} from "./view";

const POLL_MS = 10_000;
const TICK_MS = 1_000;

interface Source {
  get(): Promise<Result<Board>>;
  setAuthors(authors: string[]): Promise<Result<Board>>;
}

async function boardOf(p: Promise<{ board?: Board }>): Promise<Result<Board>> {
  const res = await fromPromise(p);
  if (res.error) return res;
  if (res.value.board === undefined) {
    return fail(new MagusError({ code: "", message: "the server answered without a board" }));
  }
  return ok(res.value.board);
}

function liveSource(host: string): Source {
  const client = createClient(PullsService, createServerTransport(host));
  return {
    get: () => boardOf(client.getPulls({})),
    setAuthors: (authors) => boardOf(client.setAuthors({ authors })),
  };
}

function demoSource(): Source {
  let current = demoBoard();
  return {
    get: () => Promise.resolve(ok(current)),
    setAuthors(authors) {
      const next = clone(BoardSchema, current);
      if (next.filter !== undefined) {
        next.filter.authors = authors;
        next.filter.isDefault = false;
        next.filter.everyAuthor = authors.length === 0;
      }
      current = next;
      return Promise.resolve(ok(current));
    },
  };
}

// The sync block changes every poll; the rest only when the forge data does.
function bodyKey(board: Board): string {
  const copy = clone(BoardSchema, board);
  copy.sync = undefined;
  return toJsonString(BoardSchema, copy);
}

export function activate(host: HTMLElement): AppInstance {
  const root = h("section", "console-pulls-app");
  root.setAttribute("aria-label", "Pull requests");
  const sync = h("p");
  sync.dataset.part = "sync";
  const problem = h("p");
  problem.setAttribute("role", "alert");
  problem.hidden = true;
  const filterLine = h("p");
  filterLine.dataset.part = "filter";
  const chips = h("div");
  const form = h("form");
  const input = h("input");
  input.type = "text";
  input.name = "author";
  input.placeholder = "add an author";
  input.setAttribute("aria-label", "Add an author");
  const add = h("button", undefined, "Add");
  add.type = "submit";
  form.append(input, " ", add);
  const mainHost = h("div");
  const patternsHost = h("div");
  const orderHost = h("div");
  const rows = h("div");
  rows.setAttribute("aria-label", "Open pull requests");
  root.append(
    h("h1", undefined, "Pulls"),
    sync,
    problem,
    filterLine,
    chips,
    form,
    mainHost,
    patternsHost,
    orderHost,
    rows,
  );
  host.append(root);

  let board: Board | null = null;
  let renderedKey = "";
  let stale = false;
  let visible = true;
  let source: Source | null = null;
  let polling = false;
  let pollTimer: ReturnType<typeof setInterval> | undefined;
  let tickTimer: ReturnType<typeof setInterval> | undefined;
  const open = new Set<number>();

  // A poll rebuilds the rows; this keeps an expanded row expanded.
  rows.addEventListener(
    "toggle",
    (e) => {
      const d = e.target;
      if (!(d instanceof HTMLDetailsElement) || d.dataset.pull === undefined) return;
      const n = Number(d.dataset.pull);
      if (d.open) open.add(n);
      else open.delete(n);
    },
    true,
  );

  function paintSync(): void {
    sync.textContent =
      board === null ? "waiting for the first board" : syncLine(board.sync, Date.now());
  }

  function paint(): void {
    paintSync();
    if (board === null) return;
    const key = bodyKey(board);
    if (key === renderedKey) return;
    renderedKey = key;
    const now = Date.now();
    filterLine.textContent = authorsLine(board.filter);
    chips.replaceChildren(renderChips(board.filter, removeAuthor));
    mainHost.replaceChildren(renderMain(board.main));
    patternsHost.replaceChildren(renderPatterns(board.patterns));
    orderHost.replaceChildren(renderOrder(board.mergeOrder));
    rows.replaceChildren(...board.pulls.map((p) => renderRow(p, now, open.has(p.number))));
    if (board.pulls.length === 0) rows.append(h("p", undefined, "No open pull requests match."));
  }

  function accept(res: Result<Board>): void {
    if (stale) return;
    if (res.error) {
      problem.textContent = "Could not read the board: " + res.error.message;
      problem.hidden = false;
      paintSync();
      return;
    }
    problem.hidden = true;
    board = res.value;
    paint();
  }

  async function poll(): Promise<void> {
    if (source === null || polling) return;
    polling = true;
    const res = await source.get();
    polling = false;
    accept(res);
  }

  async function writeAuthors(authors: string[]): Promise<void> {
    if (source === null) return;
    accept(await source.setAuthors(authors));
  }

  function removeAuthor(author: string): void {
    const current = board?.filter?.authors ?? [];
    void writeAuthors(current.filter((a) => a !== author));
  }

  form.addEventListener("submit", (e) => {
    e.preventDefault();
    const name = input.value.trim();
    if (name === "") return;
    input.value = "";
    const current = board?.filter?.authors ?? [];
    if (current.includes(name)) return;
    void writeAuthors([...current, name]);
  });

  const params = parseHash();
  consumeLiveToken(params);
  adoptServerOrigin();
  const demo = wantsDemo(params);

  function stop(): void {
    clearInterval(pollTimer);
    clearInterval(tickTimer);
  }

  function start(): void {
    stop();
    if (!visible || source === null) return;
    tickTimer = setInterval(paintSync, TICK_MS);
    if (!demo) pollTimer = setInterval(() => void poll(), POLL_MS);
  }

  if (demo) {
    source = demoSource();
  } else {
    const serverHost = resolveServerHostOrRemembered(params);
    if (serverHost === null) {
      problem.textContent = "No magus server address is set, so there is no board to read.";
      problem.hidden = false;
    } else {
      source = liveSource(serverHost);
    }
  }
  paintSync();
  void poll();
  start();

  return {
    setVisible(v: boolean): void {
      visible = v;
      if (v) {
        void poll();
        start();
      } else {
        stop();
      }
    },
    deactivate(): void {
      stale = true;
      stop();
    },
  };
}
