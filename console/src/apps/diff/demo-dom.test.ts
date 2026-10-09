// demo-dom.test.ts - the Diff app's server-free showcase, mounted. document/window come
// from test-setup.mjs (node --import), the same as the other *-dom tests.
//
// What is pinned here is the promise the #demo fragment makes: the app renders FULLY with
// no server, no workspace and no network. So fetch is replaced with one that fails the test if
// it is called at all - a showcase that quietly falls back to a server works on the machine
// that has one running and shows an empty state to everybody else, which is the failure this
// mode exists to prevent.

import assert from "node:assert/strict";
import { test, beforeEach, afterEach } from "node:test";
import { activate } from "./main";
import { dispatchCommand } from "../../desktop/commands";
import { DEMO_RUN_MS } from "./demo";

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

// readoutItems is what the readout row says: its labels and its counted items, as text.
function readoutItems(): string[] {
  return [
    ...document.querySelectorAll(
      ".console-diff-toolbar__stats .pf-v6-c-label, .console-diff-toolbar__stats [data-stat]",
    ),
  ].map((el) => el.textContent ?? "");
}

test("#demo renders the changeset with no server", async () => {
  location.hash = "#demo";
  const dispose = activate(document.body);
  await settle();

  const root = document.querySelector<HTMLElement>(".console-diff-layout");
  assert.equal(root?.dataset.phase, "ready");

  const paths = [...document.querySelectorAll(".console-diff-row__path")].map(
    (el) => el.textContent,
  );
  assert.equal(paths[0], "libs/authkit/claims.go");

  // Rows the server's annotations produce, not the patch's: a touch row is one, and the
  // text row proves the patch body reached the virtualizer.
  assert.ok(document.querySelector(".console-diff-row--touch"));
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

  const chips = readoutItems();
  // The showcase must never pass itself off as the reader's own tree - but it says so through
  // the shell's connection pill, the one place every app says it. A second badge here made
  // the diff the only app announcing demo twice, in a style nothing else uses.
  assert.ok(!chips.includes("demo data"), "demo state belongs to the connection pill, not a chip");
  assert.ok(chips.includes("11 files"));
  assert.ok(chips.includes("1 public API"));
  assert.ok(chips.includes("1 untested"));
  // The ranking key is present in the fixture, so the app must NOT be wearing the
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

  // Skipping answers the suggestion in memory, so the rail has to shrink with no server in it.
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

// The app used to carry its own "See the demo" button. It does not any more - one entry point for
// the whole console, the title bar's Workspace menu - so what it owes someone with no server is a
// SENTENCE naming where a populated version lives, not a dead end. Every /console/<app>/ path is
// the shell with a <base> injected (scripts/app-stubs.mjs), so that menu is always on screen.
test("without #demo and without a server the app says where a populated one lives", async () => {
  const dispose = activate(document.body);
  await settle();

  const root = document.querySelector<HTMLElement>(".console-diff-layout");
  assert.equal(root?.dataset.phase, "empty");
  assert.equal(
    document.querySelectorAll(".pf-v6-c-empty-state__footer button").length,
    0,
    "the per-app demo button is gone",
  );
  const body = document.querySelector<HTMLElement>(".pf-v6-c-empty-state__body")?.textContent ?? "";
  assert.match(body, /Workspace menu/, "an empty app has to name where a populated one lives");

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

// The pane-width defaults. These key off the app's OWN box, not the viewport, because the
// app tiles: two panes on a wide desktop give it far less room than the window suggests, and a
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
    assert.ok(paneCb, "the app must observe its own root");
    // happy-dom reports innerWidth 1024, so the app mounts in its wide defaults.
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

    // A zero box is a detached or hidden app, not a measurement, and must not collapse it.
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

  const chips = readoutItems();
  assert.ok(chips.includes("#482"), "the open review is named");
  // One human comment in the fixture is already published, so the draft count is what is left
  // to send rather than everything the reader has written.
  assert.ok(chips.includes("1 draft"), `want one draft, got ${chips.join(", ")}`);
  // A thread on a file this changeset does not touch has nowhere in the stream to sit. It is
  // counted rather than dropped: "your colleague said nothing" is the one thing this app
  // must never say by accident.
  assert.ok(chips.includes("1 comment elsewhere"));

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
  assert.match(box?.textContent ?? "", /Send 1 comment to acme\/acme #482/);
  // And the network, said out loud. Everything else on this app is local; the one act that
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
  const chips = readoutItems();
  assert.ok(
    !chips.some((c) => c?.endsWith("draft") || c?.endsWith("drafts")),
    `nothing is left to send, got ${chips.join(", ")}`,
  );

  dispose.deactivate();
});

// setFocusMode puts the app in a known mode instead of assuming one.
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

// Focus mode: one step, and a pass that says where it is. The counts describe the WHOLE
// changeset while the stream shows one step, which is the part worth pinning - a progress line
// computed from what is on screen would read "step 1 of 1" forever.
test("#demo focus mode shows one step and counts the whole pass", async () => {
  location.hash = "#demo";
  const dispose = activate(document.body);
  await settle();

  await setFocusMode(true);

  const root = document.querySelector<HTMLElement>(".console-diff-layout");
  assert.equal(root?.dataset.focus, "on");
  assert.equal(
    document.querySelector(".console-diff-progress__text")?.textContent,
    "Step 1 of 12",
    "the pass is counted over the changeset, not over what is on screen",
  );
  const bar = document.querySelector(".console-diff-progress [role=progressbar]");
  assert.equal(
    bar?.getAttribute("aria-valuetext"),
    "0 of 14 hunks read",
    "and the bar counts hunks read",
  );
  assert.equal(bar?.getAttribute("aria-valuemax"), "14");
  // The first step is the two hunks of the claims contract, shown together: the heading names
  // the group and the hunk count, and the hunk under it carries the server's reason. (The second
  // hunk starts below the virtualized window, so only the heading proves it is in the step.)
  assert.match(
    document.querySelector(".console-diff-row--step")?.textContent ?? "",
    /Step 1 of 12.*Claims.*2 hunks/,
  );
  assert.match(
    document.querySelector(".console-diff-row--why")?.textContent ?? "",
    /defines Claims; the steps below use it/,
  );
  assert.equal(
    document.querySelectorAll(".console-diff-row--step").length,
    1,
    "one heading for the step, not one per hunk",
  );

  await setFocusMode(false);
  dispose.deactivate();
});

// The order's later groups follow the connected ones, and the reason each hunk carries is the
// server's sentence rather than anything the console derived.
test("#demo focus mode walks the generated and unplaced groups after the connected ones", async () => {
  location.hash = "#demo";
  const dispose = activate(document.body);
  await settle();

  await setFocusMode(true);
  for (let i = 0; i < 7; i++) {
    assert.ok(dispatchCommand("diff.hunk.next"));
    await settle();
  }
  const step = document.querySelector<HTMLElement>(".console-diff-row--step");
  assert.equal(step?.dataset.group, "generated");
  assert.match(step?.textContent ?? "", /Step 8 of 12.*generated output/);
  assert.match(
    document.querySelector(".console-diff-row--why")?.textContent ?? "",
    /generated output, read after the source that produced it/,
  );

  for (let i = 0; i < 3; i++) {
    assert.ok(dispatchCommand("diff.hunk.next"));
    await settle();
  }
  assert.equal(
    document.querySelector<HTMLElement>(".console-diff-row--step")?.dataset.group,
    "unranked",
  );

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

  assert.equal(document.querySelector(".console-diff-progress__text")?.textContent, "Step 2 of 12");
  assert.equal(
    document
      .querySelector(".console-diff-progress [role=progressbar]")
      ?.getAttribute("aria-valuetext"),
    "2 of 14 hunks read",
    "one key marks every hunk of this step and moves to the next",
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
  assert.match(text, /Comments on the review, elsewhere/);
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

  const chips = readoutItems();
  assert.ok(
    !chips.some((c) => c?.endsWith("draft") || c?.endsWith("drafts")),
    `the discarded comment is gone, got ${chips.join(", ")}`,
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

// The run control is the one review capability a forge structurally cannot offer: it asks the
// machine the code is on. It is in the showcase for the reason every other control is - the
// showcase IS the app, so a button that does nothing here reads as a broken feature - and
// these pin the two claims it makes that are easy to get wrong and invisible when wrong.

test("#demo offers to run the project in view", async () => {
  location.hash = "#demo";
  const dispose = activate(document.body);
  await settle();

  const button = document.querySelector<HTMLButtonElement>(".console-diff-toolbar__verdict");
  assert.ok(button, "the verdict control is absent");
  assert.equal(button.hidden, false);
  assert.match(button.textContent ?? "", /^Test /, `got ${button.textContent}`);
  // Never a verdict before anything ran: "unknown" and "passed" must not render alike.
  assert.equal(button.dataset.state, "unknown");

  dispose.deactivate();
});

test("#demo reports a run in flight, then its verdict", async () => {
  location.hash = "#demo";
  const dispose = activate(document.body);
  await settle();

  const button = document.querySelector<HTMLButtonElement>(".console-diff-toolbar__verdict");
  assert.ok(button);
  button.click();
  await settle();

  // In flight: the control says so and refuses a second press, because a run already running is
  // joined rather than started again.
  assert.equal(button.dataset.state, "running");
  assert.equal(button.disabled, true);
  assert.match(button.textContent ?? "", /Testing /);

  await new Promise((r) => setTimeout(r, DEMO_RUN_MS + 50));
  await settle();

  assert.equal(button.dataset.state, "passed");
  assert.equal(button.disabled, false);
  assert.match(button.textContent ?? "", /passed/);
  // The duration is the evidence the run happened rather than being looked up.
  assert.match(button.textContent ?? "", /\d+\.\ds/);

  dispose.deactivate();
});

// The verdict control is the reachable half of the approval rule. The server decides what is
// allowed and refuses anything else at publish time, but a rule nobody can invoke is a feature
// that never fires - which is the failure these pin.

test("#demo offers every verdict the server allowed", async () => {
  location.hash = "#demo";
  const dispose = activate(document.body);
  await settle();

  assert.ok(dispatchCommand("diff.publish"));
  await settle();

  const choices = [
    ...document.querySelectorAll<HTMLInputElement>(".console-diff-composer__verdict input"),
  ];
  assert.ok(
    document.querySelector(".console-diff-composer__verdict.pf-v6-c-radio"),
    "the choices are PF radios, each with its label",
  );
  assert.deepEqual(
    choices.map((c) => c.value),
    ["comment", "approve", "request_changes"],
    "the showcase reviews somebody else's change, so all three are offered",
  );
  // Remarks is what a reader gets by doing nothing. Approving has to be a choice they made.
  assert.deepEqual(
    choices.filter((c) => c.checked).map((c) => c.value),
    ["comment"],
  );

  dispose.deactivate();
});

// The row is present even when only remarks are allowed, so its absence can never hide a bug -
// and no reason is shown when nothing was limited.
test("#demo gives no reason when nothing narrowed the verdicts", async () => {
  location.hash = "#demo";
  const dispose = activate(document.body);
  await settle();

  assert.ok(dispatchCommand("diff.publish"));
  await settle();

  assert.ok(document.querySelector(".console-diff-composer__verdicts"));
  assert.equal(document.querySelectorAll(".console-diff-composer__verdictnote").length, 0);

  dispose.deactivate();
});

// The toolbar STACKS, and that is the whole reason this is pinned structurally. An item appended
// straight to it is stretched to the toolbar's full width, so a button lands as a centred caption
// in a row of its own - which is how the verdict and the focus toggle each took a full row, and
// how the key legend's "sits at the trailing edge" auto margin ended up with no row to sit in.
// jsdom computes no layout, so the sibling relationship is what a test can hold; it is also the
// thing that was actually wrong.
//
// It also pins the two ZONES apart. The head carries the app's identity and its actions, the
// readout carries the counts and the key legend; putting an action back among the chips is the
// regression this row was restructured to undo.
test("the toolbar's controls sit in a row, actions apart from the readout", async () => {
  location.hash = "#demo";
  const dispose = activate(document.body);
  await settle();

  const zones: [string, string[]][] = [
    [
      "console-diff-toolbar__actions",
      [
        "console-diff-toolbar__verdict",
        "console-diff-toolbar__overview",
        "console-diff-toolbar__mode",
        "console-diff-toolbar__focus",
      ],
    ],
    [
      "console-diff-toolbar__readout",
      ["console-diff-toolbar__stats", "console-diff-toolbar__keyswrap"],
    ],
    // The legend itself sits behind a popover, not loose in the readout.
    ["console-diff-toolbar__keyswrap", ["console-diff-popover"]],
  ];
  for (const [row, members] of zones) {
    const parent = document.querySelector(`.${row}`);
    assert.ok(parent, `${row} exists`);
    for (const cls of members) {
      // Looked up inside the row, and compared as a boolean: a failed assert.equal on two DOM
      // nodes prints both trees, which does not finish.
      const el = parent.querySelector(`:scope > .${cls}`);
      assert.ok(el, `${cls} is rendered directly in ${row}, not the stack`);
    }
  }

  dispose.deactivate();
});

// Focus mode collapses the toolbar rather than emptying it. Hiding the counts alone left the
// readout standing at its own min-block-size with the key disclosure floating in an otherwise
// blank band - a mode whose claim is less chrome, spending a row on nothing. jsdom computes no
// layout, so what a test can hold is the row being hidden and the legend having moved to the row
// that is still on screen; the band itself is what the committed console-diff-focus shot shows.
test("focus mode hides the readout row and takes the key legend with it", async () => {
  location.hash = "#demo";
  const dispose = activate(document.body);
  await settle();

  const readout = document.querySelector<HTMLElement>(".console-diff-toolbar__readout");
  const keys = document.querySelector<HTMLElement>(".console-diff-toolbar__keyswrap");
  const progress = document.querySelector<HTMLElement>(".console-diff-progress");
  assert.ok(readout && keys && progress);
  assert.equal(readout.hidden, false);
  assert.ok(keys.parentElement === readout, "the legend rides the readout in the dense view");

  await setFocusMode(true);
  assert.equal(readout.hidden, true, "the row goes, not just the chips inside it");
  assert.equal(progress.hidden, false);
  assert.ok(progress.contains(keys), "the legend moves to the row that is still drawn");

  await setFocusMode(false);
  assert.equal(readout.hidden, false);
  assert.ok(keys.parentElement === readout, "and comes back with it");

  dispose.deactivate();
});

// The head is the only place the app says what it is and what it is comparing. Both were
// missing outright: the diff was the one app opening with an unheaded row of numbers, and
// nothing on it named the two sides of the diff being read.
test("the head names the app and the two sides being compared", async () => {
  location.hash = "#demo";
  const dispose = activate(document.body);
  await settle();

  const head = document.querySelector(".console-diff-toolbar__head");
  assert.equal(head?.querySelector(".console-diff-toolbar__eyebrow")?.textContent, "Review");
  // The demo session's base is "working", the STATE the server reports for the uncommitted tree
  // (types.Diff.Base) - so both sides have to be spelled out rather than echoed.
  assert.equal(
    head?.querySelector(".console-diff-toolbar__scope")?.textContent,
    "working tree vs HEAD",
  );

  dispose.deactivate();
});

// A conversation on the review is its root and then its replies, indented under it. The wire is
// flat, so this is the grouping done at render time; the first hunk of claims.go carries one.
test("#demo renders a conversation as a root with its replies indented beneath it", async () => {
  location.hash = "#demo";
  const dispose = activate(document.body);
  await settle();

  const rows = [
    ...document.querySelectorAll<HTMLElement>('.console-diff-row[data-author="review"]'),
  ];
  const ids = rows.map((el) => el.dataset.commentId);
  const at = ids.indexOf("th1");
  assert.deepEqual(
    ids.slice(at, at + 3),
    ["th1", "th1-a", "th1-b"],
    "root first, replies oldest first",
  );
  assert.equal(rows[at]?.hasAttribute("data-reply"), false);
  assert.equal(rows[at + 1]?.hasAttribute("data-reply"), true);
  assert.equal(rows[at + 2]?.hasAttribute("data-reply"), true);
  // One Reply for the conversation, on its root, and none on the replies.
  const buttons = [...document.querySelectorAll<HTMLButtonElement>(".console-diff-row__reply")];
  assert.equal(buttons.filter((b) => b.dataset.threadId === "th1").length, 1);
  assert.equal(rows[at + 1]?.querySelector(".console-diff-row__reply"), null);

  dispose.deactivate();
});

// A remark whose line has gone from the head: the host's own text of the hunk stands in for the
// code. It sits on verify.go, which the reading order reaches at its third step; focus mode keeps
// the stream to that step, so the rows are within the virtualized window.
test("#demo marks an outdated conversation and quotes the code it was about", async () => {
  location.hash = "#demo";
  const dispose = activate(document.body);
  await settle();

  await setFocusMode(true);
  for (let i = 0; i < 2; i++) {
    assert.ok(dispatchCommand("diff.hunk.next"));
    await settle();
  }
  const outdated = document.querySelector<HTMLElement>('.console-diff-row[data-comment-id="th5"]');
  assert.ok(outdated, "the outdated remark is on the stream, not dropped");
  assert.match(outdated.textContent ?? "", /outdated/);
  const quoted = [...document.querySelectorAll(".console-diff-row--quote")].map(
    (el) => el.textContent,
  );
  assert.ok(quoted.some((t) => t?.includes("claims.Audience != v.service && claims.Scope")));

  await setFocusMode(false);
  dispose.deactivate();
});

// The button and the `a` key are the same act on the same conversation. The reply lands in the
// conversation it answers (its root), not beside it as a new one.
test("#demo's Reply button answers the conversation by its root", async () => {
  location.hash = "#demo";
  const dispose = activate(document.body);
  await settle();

  const button = document.querySelector<HTMLButtonElement>(
    '.console-diff-row__reply[data-thread-id="th1"]',
  );
  assert.ok(button);
  button.click();
  const box = document.querySelector<HTMLElement>(".console-diff-composer");
  assert.match(box?.textContent ?? "", /Reply to priya/);
  const field = box?.querySelector<HTMLTextAreaElement>("textarea");
  assert.ok(field);
  field.value = "agreed";
  field.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", metaKey: true, bubbles: true }));
  await settle();

  const ids = [
    ...document.querySelectorAll<HTMLElement>('.console-diff-row[data-author="review"]'),
  ].map((el) => el.dataset.commentId ?? "");
  const at = ids.indexOf("th1");
  assert.equal(
    ids[at + 3]?.startsWith("th1-r"),
    true,
    `the reply follows the whole conversation: ${ids}`,
  );
  const reply = document.querySelector<HTMLElement>(
    `.console-diff-row[data-comment-id="${ids[at + 3]}"]`,
  );
  assert.equal(reply?.hasAttribute("data-reply"), true);

  dispose.deactivate();
});

// The reading mark: pressed state, since when, and a command the person may copy. It is shown in
// a read-only element and never run from here.
test("#demo's reading toggle shows since when and the command to copy", async () => {
  location.hash = "#demo";
  const dispose = activate(document.body);
  await settle();

  const toggle = document.querySelector<HTMLButtonElement>(".console-diff-toolbar__reading");
  assert.ok(toggle);
  assert.equal(toggle.getAttribute("aria-pressed"), "false");
  assert.equal(document.querySelector<HTMLElement>(".console-diff-reading")?.hidden, true);

  toggle.click();
  await settle();
  assert.equal(toggle.getAttribute("aria-pressed"), "true");
  assert.ok(
    toggle.classList.contains("pf-m-selected"),
    "the pressed state is drawn by PF's toggle group, not by a border of our own",
  );
  const strip = document.querySelector<HTMLElement>(".console-diff-reading");
  assert.equal(strip?.hidden, false);
  const since = strip?.querySelector<HTMLElement>(".console-diff-reading__since");
  assert.match(since?.textContent ?? "", /^reading since /);
  // The command is in a popover off the head, open as the mark turns on.
  assert.equal(since?.getAttribute("aria-expanded"), "true");
  const command = strip?.querySelector<HTMLElement>("code.pf-v6-c-clipboard-copy__text");
  assert.match(command?.textContent ?? "", /^gh pr comment 482 --repo acme\/acme/);
  assert.equal(command?.isContentEditable, false);
  assert.equal(
    strip?.querySelector("button.pf-m-plain[aria-label='Copy command']")?.textContent,
    "",
    "an icon button: its name is the aria-label",
  );

  toggle.click();
  await settle();
  assert.equal(toggle.getAttribute("aria-pressed"), "false");
  assert.equal(document.querySelector<HTMLElement>(".console-diff-reading")?.hidden, true);

  dispose.deactivate();
});
