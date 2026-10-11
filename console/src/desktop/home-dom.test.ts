// home-dom.test.ts - what the launcher RENDERS. The live reading has its own file; this one pins the
// parts a screen reader and a keyboard meet: one stable heading, a list of cards, a card whose title is
// the button that opens it, and a kebab that is a real PF menu rather than a menu hand-built inside a
// button.

import assert from "node:assert/strict";
import { beforeEach, describe, test } from "node:test";
import { buildLauncher, LAUNCHER_LEDE, LAUNCHER_TITLE, syncLauncherDemo } from "./home";
import { dashboard } from "../apps/dashboard/app";
import { logs } from "../apps/logs/app";

const APPS = [dashboard, logs];

describe("the launcher", () => {
  let root: HTMLElement;
  const opened: string[] = [];

  beforeEach(() => {
    opened.length = 0;
    document.body.replaceChildren();
    root = buildLauncher(APPS, (id) => opened.push(id));
    document.body.append(root);
  });

  // A heading that changed on every load could not be found by title, and a screen reader heard a
  // different page each visit.
  test("says the same thing on every load", () => {
    const again = buildLauncher(APPS, () => {});
    assert.equal(root.querySelector("h1")?.textContent, LAUNCHER_TITLE);
    assert.equal(again.querySelector("h1")?.textContent, LAUNCHER_TITLE);
    assert.equal(root.querySelector("h1 + p")?.textContent, LAUNCHER_LEDE);
  });

  test("lists the apps as a list, one item each", () => {
    const list = root.querySelector("ul.pf-v6-l-gallery");
    assert.ok(list, "the cards are a list");
    assert.equal(list.getAttribute("aria-label"), "Apps");
    assert.deepEqual(
      [...list.children].map((li) => li.tagName),
      ["LI", "LI"],
    );
  });

  // The title is what names the card's button, so a reader hears "Dashboard, button" once. The card
  // itself is no longer a button holding another button.
  test("a card's clickable action is a button named by its title", () => {
    const card = root.querySelector<HTMLElement>('[data-open="dashboard"]');
    assert.ok(card);
    assert.equal(card.getAttribute("role"), null, "the card is not itself a button");
    const action = card.querySelector<HTMLButtonElement>(".pf-v6-c-card__clickable-action");
    assert.ok(action);
    const title = card.querySelector("h2.pf-v6-c-card__title-text");
    assert.equal(action.getAttribute("aria-labelledby"), title?.id);
    assert.equal(title?.textContent, dashboard.label);
    assert.ok(
      card.querySelector(`#${action.getAttribute("aria-describedby")}`),
      "the hint describes it",
    );
    action.click();
    assert.deepEqual(opened, ["dashboard"]);
  });

  test("the kebab lives in the card's header actions and opens a PF menu with the arrow keys", () => {
    const card = root.querySelector<HTMLElement>('[data-open="logs"]');
    const kebab = card?.querySelector<HTMLButtonElement>(
      ".pf-v6-c-card__actions [data-card-kebab]",
    );
    const menu = card?.querySelector<HTMLElement>(".pf-v6-c-menu");
    assert.ok(kebab && menu);
    assert.equal(menu.hidden, true);
    assert.equal(kebab.getAttribute("aria-haspopup"), "menu");
    assert.equal(kebab.getAttribute("aria-expanded"), "false");
    kebab.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true }));
    assert.equal(menu.hidden, false, "ArrowDown opens it, as the menu button pattern asks");
    assert.equal(kebab.getAttribute("aria-expanded"), "true");
    const item = menu.querySelector<HTMLElement>('[role="menuitem"]');
    assert.equal(item?.textContent, "Open in a new window");
    // Compared with ok(), not equal(): a failing equal() inspects both DOM nodes, and printing a
    // happy-dom tree is slow enough to look like a hang.
    assert.ok(document.activeElement === item, "focus lands on the first item");
    menu.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    assert.equal(menu.hidden, true);
    assert.ok(document.activeElement === kebab, "and Escape hands focus back");
  });

  // Offering to enter the place you are already in is a dead end.
  test("the demo way goes away while the console is already in the demo", () => {
    const way = root.querySelector<HTMLElement>("[data-launcher-demo]");
    assert.ok(way);
    syncLauncherDemo(root, true);
    assert.equal(way.hidden, true);
    syncLauncherDemo(root, false);
    assert.equal(way.hidden, false);
  });
});
