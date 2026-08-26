// demo-dom.test.ts - the Diff surface's daemon-free showcase, mounted. document/window come
// from test-setup.mjs (node --import), the same as the other *-dom tests.
//
// What is pinned here is the promise the #demo fragment makes: the surface renders FULLY with
// no daemon, no workspace and no network. So fetch is replaced with one that fails the test if
// it is called at all - a showcase that quietly falls back to a daemon works on the machine
// that has one running and shows an empty state to everybody else, which is the failure this
// mode exists to prevent.

import assert from "node:assert/strict";
import { test, beforeEach, afterEach } from "node:test";
import { activate } from "./main";
import { dispatchCommand } from "../commands";

const realFetch = globalThis.fetch;

beforeEach(() => {
  localStorage.clear();
  sessionStorage.clear();
  document.body.replaceChildren();
  globalThis.fetch = (() => {
    throw new Error("demo mode must not reach the network");
  }) as typeof fetch;
});

afterEach(() => {
  globalThis.fetch = realFetch;
  location.hash = "";
});

// settle lets load()'s awaits and the per-hunk digests resolve. Turns rather than a delay:
// everything under test resolves on the microtask queue.
async function settle(turns = 12): Promise<void> {
  for (let i = 0; i < turns; i++) await new Promise((r) => setTimeout(r, 0));
}

test("#demo renders the changeset with no daemon", async () => {
  location.hash = "#demo";
  const dispose = activate(document.body);
  await settle();

  const root = document.querySelector<HTMLElement>(".console-diff-layout");
  assert.equal(root?.dataset.phase, "ready");

  const paths = [...document.querySelectorAll(".console-diff-row__path")].map(
    (el) => el.textContent,
  );
  assert.equal(paths[0], "libs/authkit/claims.go");

  // Rows the daemon's annotations produce, not the patch's: a story row is a touch, and the
  // text row proves the patch body reached the virtualizer.
  assert.ok(document.querySelector(".console-diff-row--story"));
  const text = [...document.querySelectorAll(".console-diff-row__text")].map(
    (el) => el.textContent,
  );
  assert.ok(text.some((t) => t?.includes("Audience Audience")));

  dispose.deactivate();
});

test("#demo lists the primary files in the sidebar and folds the generated group", async () => {
  location.hash = "#demo";
  const dispose = activate(document.body);
  await settle();

  assert.equal(document.querySelectorAll(".console-diff-sidebar__item").length, 11);
  assert.equal(document.querySelector(".console-diff-sidebar__group")?.textContent, "3 generated");

  const chips = [...document.querySelectorAll(".console-diff-toolbar__stats .pf-v6-c-label")].map(
    (el) => el.textContent,
  );
  // The showcase must never pass itself off as the reader's own tree - but it says so through
  // the shell's connection pill, the one place every surface says it. A second badge here made
  // the diff the only surface announcing demo twice, in a style nothing else uses.
  assert.ok(!chips.includes("demo data"), "demo state belongs to the connection pill, not a chip");
  assert.ok(chips.includes("11 files"));
  assert.ok(chips.includes("1 public surface"));
  assert.ok(chips.includes("1 untested"));
  // The ranking key is present in the fixture, so the surface must NOT be wearing the
  // "unranked" caveat while claiming a reading order.
  assert.ok(!chips.includes("unranked"));

  dispose.deactivate();
});

test("#demo shows the agent's pending suggestions in the rail", async () => {
  location.hash = "#demo";
  const dispose = activate(document.body);
  await settle();

  const items = [...document.querySelectorAll(".console-diff-rail__item")];
  assert.equal(items.length, 2);
  assert.equal(items[0]?.querySelector(".console-diff-rail__who")?.textContent, "claude-code");

  // Skipping answers the suggestion in memory, so the rail has to shrink with no daemon in it.
  (items[0]?.querySelector(".console-diff-rail__skip") as HTMLButtonElement).click();
  await settle();
  assert.equal(document.querySelectorAll(".console-diff-rail__item").length, 1);

  dispose.deactivate();
});

test("#demo does not advertise workspace context it cannot provide", async () => {
  location.hash = "#demo";
  const dispose = activate(document.body);
  await settle();

  assert.equal(document.querySelectorAll(".console-diff-row__peek").length, 0);

  dispose.deactivate();
});

// The row button above is withheld on purpose, but the p key and the command palette reach the
// same action through no button at all - so a demo reader who finds either must still be told
// why nothing came up, not met with silence.
test("#demo answers the peek command instead of doing nothing", async () => {
  location.hash = "#demo";
  const dispose = activate(document.body);
  await settle();

  assert.ok(dispatchCommand("diff.context.peek"));
  const context = document.querySelector<HTMLElement>(".console-diff-context");
  assert.equal(context?.hidden, false);
  assert.match(context?.textContent ?? "", /unavailable in this showcase/);

  dispose.deactivate();
});

// The surface used to carry its own "See the demo" button. It does not any more - one entry point for
// the whole console, the title bar's Workspace menu - so what it owes someone with no daemon is a
// SENTENCE naming where a populated version lives, not a dead end. Every /console/<surface>/ path is
// the shell with a <base> injected (scripts/surface-stubs.mjs), so that menu is always on screen.
test("without #demo and without a daemon the surface says where a populated one lives", async () => {
  const dispose = activate(document.body);
  await settle();

  const root = document.querySelector<HTMLElement>(".console-diff-layout");
  assert.equal(root?.dataset.phase, "empty");
  assert.equal(
    document.querySelectorAll(".pf-v6-c-empty-state__footer button").length,
    0,
    "the per-surface demo button is gone",
  );
  const body = document.querySelector<HTMLElement>(".pf-v6-c-empty-state__body")?.textContent ?? "";
  assert.match(body, /Workspace menu/, "an empty surface has to name where a populated one lives");

  dispose.deactivate();
});

// #demo still reaches the demo with no button anywhere - the fragment is the mechanism, and the
// button was only ever one way to set it.
test("#demo still loads the fabricated changeset", async () => {
  location.hash = "#demo";
  const dispose = activate(document.body);
  await settle();

  const root = document.querySelector<HTMLElement>(".console-diff-layout");
  assert.equal(root?.dataset.phase, "ready");
  assert.ok(document.querySelector(".console-diff-row__path"));

  dispose.deactivate();
});

// The pane-width defaults. These key off the surface's OWN box, not the viewport, because the
// surface tiles: two panes on a wide desktop give it far less room than the window suggests, and a
// viewport query reads "wide" for both. happy-dom does no layout, so the ResizeObserver is stubbed
// and driven by hand - which is also the only way to exercise a retile without a real browser.
//
// The stub is restored in the same test. Isolation is `none`, so a leaked global would follow every
// other *-dom test in the process.
test("the diff sizes itself from its own pane, not the window", async () => {
  const realRO = globalThis.ResizeObserver;
  // activate() installs two observers (the sidebar virtualizer's and the pane's); keep the one
  // watching the layout root.
  let paneCb: ResizeObserverCallback | null = null;
  class CapturingRO {
    constructor(private cb: ResizeObserverCallback) {}
    observe(el: Element): void {
      if (el.classList.contains("console-diff-layout")) paneCb = this.cb;
    }
    unobserve(): void {}
    disconnect(): void {}
  }
  globalThis.ResizeObserver = CapturingRO as unknown as typeof ResizeObserver;

  try {
    location.hash = "#demo";
    const dispose = activate(document.body);
    await settle();

    const root = document.querySelector<HTMLElement>(".console-diff-layout");
    assert.ok(paneCb, "the surface must observe its own root");
    // happy-dom reports innerWidth 1024, so the surface mounts in its wide defaults.
    assert.equal(root?.dataset.sidebar, "open");

    const resizeTo = async (width: number): Promise<void> => {
      (paneCb as ResizeObserverCallback)(
        [{ contentRect: { width } } as ResizeObserverEntry],
        {} as ResizeObserver,
      );
      await settle();
    };

    // A 700px PANE on an unchanged 1024px window: the case a viewport query cannot see.
    await resizeTo(700);
    assert.equal(root?.dataset.sidebar, "collapsed", "a narrow pane folds the index");

    // Retiled back to full width, the index returns.
    await resizeTo(1200);
    assert.equal(root?.dataset.sidebar, "open", "a wide pane restores the index");

    // A zero box is a detached or hidden surface, not a measurement, and must not collapse it.
    await resizeTo(0);
    assert.equal(root?.dataset.sidebar, "open", "an unlaid-out box is not a narrow one");

    dispose.deactivate();
  } finally {
    globalThis.ResizeObserver = realRO;
  }
});

// The review half, mounted. Everything below is the production path with the showcase's
// fabricated review: the chips, the placement of a colleague's remark against the hunks on
// screen, and the send box that shows the batch before it leaves.
test("#demo places the review's threads beside the code they are about", async () => {
  location.hash = "#demo";
  const dispose = activate(document.body);
  await settle();

  const threads = [...document.querySelectorAll('.console-diff-row[data-author="review"]')];
  assert.ok(threads.length > 0, "a colleague's remark has to reach the stream");

  const chips = [...document.querySelectorAll(".console-diff-toolbar__stats .pf-v6-c-label")].map(
    (el) => el.textContent,
  );
  assert.ok(chips.includes("#482"), "the open review is named");
  // One human comment in the fixture is already published, so the draft count is what is left
  // to send rather than everything the reader has written.
  assert.ok(chips.includes("1 draft"), `want one draft, got ${chips.join(", ")}`);
  // A thread on a file this changeset does not touch has nowhere in the stream to sit. It is
  // counted rather than dropped: "your colleague said nothing" is the one thing this surface
  // must never say by accident.
  assert.ok(chips.includes("1 elsewhere"));

  dispose.deactivate();
});

test("#demo shows the batch before it sends it, and sending clears the drafts", async () => {
  location.hash = "#demo";
  const dispose = activate(document.body);
  await settle();

  assert.ok(dispatchCommand("diff.publish"));
  const box = document.querySelector<HTMLElement>(".console-diff-composer--batch");
  assert.ok(box, "publishing has to show what is about to leave");
  // The whole address, not just the review: repo and number, so "send" never means somewhere
  // the reader would have to guess.
  assert.match(box?.textContent ?? "", /Send 1 remark to acme\/acme #482/);
  // And the network, said out loud. Everything else on this surface is local; the one act that
  // leaves the machine names the host it leaves for, before it leaves.
  assert.match(box?.textContent ?? "", /Posts over the network to github\.com/);
  assert.match(box?.textContent ?? "", /Nothing has left this machine yet/);
  // The listing names WHERE each remark lands, because a host anchors an inline comment to a
  // line and a draft that cannot be placed has to be visible as such before the send.
  assert.match(box?.textContent ?? "", /libs\/authkit\/claims\.go:22/);

  const field = box?.querySelector<HTMLTextAreaElement>("textarea");
  assert.ok(field);
  field.value = "self-review pass";
  // A bare Enter is a newline here, so the send takes the chord. The next test pins that a bare
  // Enter sends nothing.
  field.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", metaKey: true, bubbles: true }));
  await settle();

  assert.equal(document.querySelector(".console-diff-composer--batch"), null, "the box closes");
  const chips = [...document.querySelectorAll(".console-diff-toolbar__stats .pf-v6-c-label")].map(
    (el) => el.textContent,
  );
  assert.ok(
    !chips.some((c) => c?.endsWith("draft") || c?.endsWith("drafts")),
    `nothing is left to send, got ${chips.join(", ")}`,
  );

  dispose.deactivate();
});

// setFocusMode puts the surface in a known mode instead of assuming one.
//
// The preference is a module-scope persisted cell, and its in-memory value is the source of
// truth - localStorage.clear() in beforeEach does not touch it. These tests share one process
// (--test-isolation=none), so a test that toggled the mode and walked away would turn it on for
// every test defined after it, in this file and the next.
async function setFocusMode(on: boolean): Promise<void> {
  const root = document.querySelector<HTMLElement>(".console-diff-layout");
  if ((root?.dataset.focus === "on") === on) return;
  assert.ok(dispatchCommand("diff.focus.toggle"));
  await settle();
}

// Focus mode: one hunk, and a pass that says where it is. The counts describe the WHOLE
// changeset while the stream shows one hunk, which is the part worth pinning - a progress line
// computed from what is on screen would read "hunk 1 of 1" forever.
test("#demo focus mode shows one hunk and counts the whole pass", async () => {
  location.hash = "#demo";
  const dispose = activate(document.body);
  await settle();

  await setFocusMode(true);

  const root = document.querySelector<HTMLElement>(".console-diff-layout");
  assert.equal(root?.dataset.focus, "on");
  assert.match(
    document.querySelector(".console-diff-progress__text")?.textContent ?? "",
    /hunk 1 of 14/,
    "the pass is counted over the changeset, not over what is on screen",
  );
  // One hunk heading in the stream is the whole claim of the mode.
  assert.equal(document.querySelectorAll(".console-diff-row--hunk").length, 1);

  await setFocusMode(false);
  dispose.deactivate();
});

// Marking read and advancing are one act. This also pins the slice: hunk 3 belongs to a
// different file than hunk 1, and its thread has to travel with it - a slice that renumbered
// hunks would render somebody else's remark here, or none at all.
test("#demo focus mode marks read and advances on one key", async () => {
  location.hash = "#demo";
  const dispose = activate(document.body);
  await settle();

  await setFocusMode(true);
  assert.ok(dispatchCommand("diff.viewed.toggle"));
  await settle();

  assert.match(
    document.querySelector(".console-diff-progress__text")?.textContent ?? "",
    /hunk 2 of 14, 1 read/,
    "one key marks this hunk and moves to the next",
  );

  await setFocusMode(false);
  dispose.deactivate();
});

// The reason the field is a textarea at all. A remark is often a paragraph and a code fence, and
// a field where Enter commits cannot hold either. Both halves are pinned here: a bare Enter must
// not send, and the chord must not be the only thing that does - a send only a chord can reach is
// a send whoever has not read the docs cannot make.
test("#demo does not send on a bare Enter, and sends from the button", async () => {
  location.hash = "#demo";
  const dispose = activate(document.body);
  await settle();

  assert.ok(dispatchCommand("diff.publish"));
  const box = document.querySelector<HTMLElement>(".console-diff-composer--batch");
  const field = box?.querySelector<HTMLTextAreaElement>("textarea");
  assert.ok(field);
  field.value = "first line";
  field.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
  await settle();
  assert.ok(
    document.querySelector(".console-diff-composer--batch"),
    "a bare Enter leaves the box open with the remark still in it",
  );

  const send = box?.querySelector<HTMLButtonElement>(".console-diff-composer__send");
  assert.ok(send, "the chord cannot be the only way to send");
  send.click();
  await settle();
  assert.equal(document.querySelector(".console-diff-composer--batch"), null, "the button sends");

  dispose.deactivate();
});

// Pressing send with nothing drafted must say so. The alternative is a key that appears broken:
// the reader presses it, no box opens, and nothing anywhere explains why.
test("#demo answers a send with nothing drafted", async () => {
  location.hash = "#demo";
  const dispose = activate(document.body);
  await settle();

  assert.ok(dispatchCommand("diff.publish"));
  const field = document.querySelector<HTMLTextAreaElement>(
    ".console-diff-composer--batch textarea",
  );
  assert.ok(field);
  field.value = "";
  field.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", metaKey: true, bubbles: true }));
  await settle();

  assert.ok(dispatchCommand("diff.publish"));
  assert.equal(document.querySelector(".console-diff-composer--batch"), null);
  assert.match(
    document.querySelector(".console-diff-collaboration")?.textContent ?? "",
    /Nothing drafted/,
  );

  dispose.deactivate();
});

// Replying is what makes this a conversation rather than a reader. The contract half existed
// in the spell from the start and nothing called it; these pin the path from a key to a thread
// that has an answer under it.
test("#demo answers the thread under the cursor", async () => {
  location.hash = "#demo";
  const dispose = activate(document.body);
  await settle();

  assert.ok(dispatchCommand("diff.thread.reply"));
  const box = document.querySelector<HTMLElement>(".console-diff-composer");
  assert.ok(box, "a thread on the first hunk has to be answerable");
  // Who is being answered, not where: a reply goes to a person, and the row above already
  // says which file.
  assert.match(box?.textContent ?? "", /Reply to priya/);

  const field = box?.querySelector<HTMLTextAreaElement>("textarea");
  assert.ok(field);
  field.value = "one service. I will pin it in the docstring.";
  field.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", metaKey: true, bubbles: true }));
  await settle();

  assert.equal(document.querySelector(".console-diff-composer"), null, "the box closes");
  const said = [...document.querySelectorAll('.console-diff-row[data-author="review"]')].map(
    (el) => el.textContent,
  );
  assert.ok(
    said.some((t) => t?.includes("I will pin it in the docstring")),
    "the reply joins the thread it answers",
  );
  dispose.deactivate();
});

// A hunk nobody has remarked on must say so rather than opening an empty box addressed to
// nobody.
test("#demo says when there is no thread here to answer", async () => {
  location.hash = "#demo";
  const dispose = activate(document.body);
  await settle();

  // Step to a file the review has not been commented on, then ask to reply.
  for (let i = 0; i < 4; i++) assert.ok(dispatchCommand("diff.file.next"));
  dispatchCommand("diff.thread.reply");
  assert.equal(document.querySelector(".console-diff-composer"), null);
  assert.match(
    document.querySelector(".console-diff-collaboration")?.textContent ?? "",
    /No thread here to answer/,
  );
  dispose.deactivate();
});

// The threads with nowhere in the stream to sit are READ in the overview, not merely counted.
// A chip saying "1 elsewhere" tells the reader something was said and withholds what, which
// leaves them worse off than not mentioning it at all.
test("#demo lets the elsewhere threads be read, not just counted", async () => {
  location.hash = "#demo";
  const dispose = activate(document.body);
  await settle();

  assert.ok(dispatchCommand("diff.overview"));
  const text = document.querySelector(".console-diff-overview")?.textContent ?? "";
  assert.match(text, /Said on the review, elsewhere/);
  assert.match(text, /scope-only tokens on the health path/);
  dispose.deactivate();
});

// Staging is only half a transaction: a remark you have changed your mind about must not have
// to be SENT to get rid of it. Discarding is local and reaches no network at all.
test("#demo lets a staged remark be discarded without sending it", async () => {
  location.hash = "#demo";
  const dispose = activate(document.body);
  await settle();

  assert.ok(dispatchCommand("diff.publish"));
  const drop = document.querySelector<HTMLButtonElement>(".console-diff-composer__drop");
  assert.ok(drop, "every staged remark offers a way out");
  drop.click();
  await settle();

  const chips = [...document.querySelectorAll(".console-diff-toolbar__stats .pf-v6-c-label")].map(
    (el) => el.textContent,
  );
  assert.ok(
    !chips.some((c) => c?.endsWith("draft") || c?.endsWith("drafts")),
    `the discarded remark is gone, got ${chips.join(", ")}`,
  );
});

// The reply is the second act that leaves the machine, and it names its destination for the
// same reason the send box does.
test("#demo names the network destination before a reply is sent", async () => {
  location.hash = "#demo";
  const dispose = activate(document.body);
  await settle();

  assert.ok(dispatchCommand("diff.thread.reply"));
  const box = document.querySelector<HTMLElement>(".console-diff-composer");
  assert.match(box?.textContent ?? "", /Reply to priya on acme\/acme #482/);
  assert.match(box?.textContent ?? "", /Posts over the network to github\.com/);

  dispose.deactivate();
});
