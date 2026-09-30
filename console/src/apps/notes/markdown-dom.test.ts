// markdown-dom.test.ts - the note body renderer. Needs a DOM (it builds nodes rather than a
// string), so it runs under the -dom suite. Run: `magus run test console`.

import { test } from "node:test";
import assert from "node:assert/strict";
import { renderMarkdown } from "./markdown";

function html(src: string): string {
  const host = document.createElement("div");
  host.append(renderMarkdown(src));
  return host.innerHTML;
}

function textOf(src: string): string {
  const host = document.createElement("div");
  host.append(renderMarkdown(src));
  return host.textContent ?? "";
}

// The reason this module exists. A file hard-wrapped in someone's editor must reflow to the
// reader's pane instead of breaking mid-sentence wherever the author's column happened to fall.
test("a hard-wrapped paragraph reflows into one paragraph", () => {
  const out = html("Keying on the whole tree was tried first\nand abandoned. It is useless.");
  assert.equal(
    out,
    '<p class="console-notes-app__md-p">Keying on the whole tree was tried first and abandoned. It is useless.</p>',
  );
});

test("a blank line starts a new paragraph", () => {
  const host = document.createElement("div");
  host.append(renderMarkdown("First para\nwrapped.\n\nSecond para."));
  assert.equal(host.querySelectorAll("p").length, 2);
  assert.equal(host.querySelectorAll("p")[0]?.textContent, "First para wrapped.");
});

// Untrusted by contract. The guarantee is structural - every node is createElement plus
// textContent, so there is never a string of markup for this to ride in on.
test("raw HTML in a note is text, not markup", () => {
  const host = document.createElement("div");
  host.append(renderMarkdown("<script>alert(1)</script>\n\n<img src=x onerror=alert(1)>"));
  assert.equal(host.querySelector("script"), null, "no script element is ever created");
  assert.equal(host.querySelector("img"), null, "and no img");
  assert.match(
    host.textContent ?? "",
    /<script>alert\(1\)<\/script>/,
    "it renders as its characters",
  );
});

test("a javascript: link keeps its label and is not a link", () => {
  const host = document.createElement("div");
  host.append(renderMarkdown("see [the docs](javascript:alert(1)) for more"));
  assert.equal(host.querySelector("a"), null, "an unsafe scheme never becomes an anchor");
  assert.match(host.textContent ?? "", /see the docs for more/, "the words stay in the sentence");
});

test("an http link renders as a link", () => {
  const host = document.createElement("div");
  host.append(renderMarkdown("see [the docs](https://example.com/x) for more"));
  const a = host.querySelector("a");
  assert.ok(a);
  assert.equal(a.getAttribute("href"), "https://example.com/x");
  assert.equal(a.textContent, "the docs");
  assert.equal(a.getAttribute("rel"), "noreferrer noopener");
});

// The case paragraph-reflow alone would destroy: joining every newline turns a list into one
// run-on sentence, which is why this needs a block parser rather than a join.
test("a list stays a list", () => {
  const host = document.createElement("div");
  host.append(
    renderMarkdown(
      "Reasons:\n\n- first reason\n- second reason\n  wrapped onto two lines\n- third",
    ),
  );
  const items = [...host.querySelectorAll("li")].map((li) => li.textContent);
  assert.deepEqual(items, ["first reason", "second reason wrapped onto two lines", "third"]);
});

test("an ordered list keeps its element", () => {
  const host = document.createElement("div");
  host.append(renderMarkdown("1. one\n2. two"));
  assert.ok(host.querySelector("ol"));
  assert.equal(host.querySelectorAll("li").length, 2);
});

// Code is literal: a line inside a fence that looks like a heading or a bullet is neither.
test("fenced code is verbatim and keeps its newlines", () => {
  const host = document.createElement("div");
  host.append(renderMarkdown("```\n# not a heading\n- not a bullet\n```"));
  const code = host.querySelector("pre code");
  assert.ok(code);
  assert.equal(code.textContent, "# not a heading\n- not a bullet");
  assert.equal(host.querySelectorAll("h1,h2,h3,h4,h5,h6").length, 0);
  assert.equal(host.querySelectorAll("li").length, 0);
});

// A note's own title is the h2 above the prose, so a `#` inside the body is a section within
// the note rather than a peer of its title.
test("headings are demoted beneath the note title", () => {
  const host = document.createElement("div");
  host.append(renderMarkdown("# Top\n\n## Second"));
  assert.ok(host.querySelector("h3"));
  assert.ok(host.querySelector("h4"));
  assert.equal(host.querySelector("h1"), null);
});

test("a setext heading is a heading, not a rule", () => {
  const host = document.createElement("div");
  host.append(
    renderMarkdown("internal/cache/key.go hunk 3\n----------------------------\n\nprose"),
  );
  assert.equal(host.querySelector("h4")?.textContent, "internal/cache/key.go hunk 3");
  assert.equal(host.querySelector("hr"), null);
});

test("inline code and emphasis render as elements", () => {
  const host = document.createElement("div");
  host.append(renderMarkdown("run `magus notes verify` and **do not** skip it"));
  assert.equal(host.querySelector("code")?.textContent, "magus notes verify");
  assert.equal(host.querySelector("strong")?.textContent, "do not");
});

// Anything unrecognized keeps its characters. A note losing a line is far worse than a note
// showing a stray asterisk.
test("unrecognized syntax degrades to its own text", () => {
  assert.match(textOf("a | table | row\n---|---|---"), /a \| table \| row/);
  assert.match(textOf("some *unclosed emphasis here"), /some \*unclosed emphasis here/);
});

test("an empty body renders nothing", () => {
  assert.equal(html(""), "");
  assert.equal(html("\n\n  \n"), "");
});
