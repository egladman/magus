import assert from "node:assert/strict";
import { test } from "node:test";
import { renderMarkdown } from "./markdown";

test("renders the syntax a remark actually uses", () => {
  const html = renderMarkdown("**bold** and `code` and a [link](https://example.com)");
  assert.match(html, /<strong>bold<\/strong>/);
  assert.match(html, /<code>code<\/code>/);
  assert.match(html, /<a href="https:\/\/example\.com">link<\/a>/);
});

test("a fenced block survives as a block, not as three backticks", () => {
  const html = renderMarkdown("```go\nfunc main() {}\n```");
  assert.match(html, /<pre>/);
  assert.match(html, /func main\(\)/);
});

// A single newline is a line break to whoever typed it. The CommonMark rule would join the two
// lines into one paragraph and quietly lose the break.
test("a single newline is a line break", () => {
  assert.match(renderMarkdown("one\ntwo"), /<br\s*\/?>/);
});

// The bodies here are written by other people and arrive over the network. marked emits an html
// token verbatim by default, and the console shares an origin with the daemon's bearer token.
test("raw HTML is read back as text rather than run", () => {
  const html = renderMarkdown("<script>alert(1)</script>");
  assert.ok(!html.includes("<script>"), `a script tag must not survive, got ${html}`);
  assert.match(html, /&lt;script&gt;/);
});

test("an img tag in raw HTML does not become a request", () => {
  const html = renderMarkdown('<img src="https://tracker.example/pixel.gif">');
  assert.ok(!/<img/i.test(html), `no img element may survive, got ${html}`);
});

// A markdown image is a request the reader's browser makes to a host they did not choose.
test("a markdown image renders as its alt text", () => {
  const html = renderMarkdown("![the alt](https://tracker.example/pixel.gif)");
  assert.ok(!/<img/i.test(html), `got ${html}`);
  assert.match(html, /the alt/);
});

test("a javascript: link is not a link", () => {
  const html = renderMarkdown("[click me](javascript:alert(1))");
  assert.ok(!/<a /i.test(html), `got ${html}`);
  assert.match(html, /click me/);
});

test("a data: link is not a link", () => {
  const html = renderMarkdown("[x](data:text/html;base64,PHNjcmlwdD4=)");
  assert.ok(!/<a /i.test(html), `got ${html}`);
});

// The scheme check runs on the trimmed href, because a leading space or newline is enough to
// slip past a naive prefix test while the browser still resolves the scheme.
test("whitespace does not smuggle a scheme past the check", () => {
  const html = renderMarkdown("[x](<  javascript:alert(1)>)");
  assert.ok(!/<a /i.test(html), `got ${html}`);
});

test("an ordinary link keeps working", () => {
  assert.match(
    renderMarkdown("[docs](https://example.com/a)"),
    /<a href="https:\/\/example\.com\/a">/,
  );
});
