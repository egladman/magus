// markdown.ts - renders a review remark to HTML.
//
// Remarks are markdown wherever they end up. A colleague writes one on the host in a box that
// renders it, and the console showed the raw syntax back: asterisks around a word nobody
// emphasized, a code fence as three literal backticks. So the round trip lied in both
// directions - what you read here was not what they wrote, and what you typed here was not what
// your colleague would see.
//
// # What is neutralized, and why
//
// These bodies are written by OTHER PEOPLE and arrive over the network from the review host.
// marked emits an `html` token's source VERBATIM by default, so a remark carrying a <script>
// tag would run it in the console, with the daemon's bearer token in the same origin. Three
// things are turned off here rather than trusted:
//
//   - raw HTML, which is escaped and read back as the text it is;
//   - a link whose scheme is not http, https or mailto, so `javascript:` cannot ride a remark;
//   - images, which render as their alt text - a remote image in a remark is a request the
//     reader's browser makes to a host they did not choose, which is a tracking pixel by
//     another name.
//
// This is a hardened renderer, not a sanitizer, and the distinction is worth keeping: it covers
// the ways marked itself emits markup. If this ever needs to render a body from somewhere with
// a wider threat model, sanitize the OUTPUT rather than adding another override here.

import { marked, Renderer } from "marked";

const escapeHtml = (s: string): string =>
  s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");

// Anything else - javascript:, data:, vbscript: - reads as text rather than becoming a link.
const SAFE_SCHEME = /^(?:https?:|mailto:)/i;

const renderer = new Renderer();
renderer.html = ({ text }) => escapeHtml(text);
renderer.image = ({ text, title }) => escapeHtml(text || title || "");
const linkAsWritten = renderer.link.bind(renderer);
renderer.link = (token) =>
  SAFE_SCHEME.test(token.href.trim()) ? linkAsWritten(token) : escapeHtml(token.text);

// renderMarkdown turns one remark's body into HTML.
//
// `breaks` because a review remark is a message, not a document: a single newline is a line
// break to whoever typed it, and GitHub's own comment boxes read it that way. Rendering
// paragraphs by the CommonMark rule instead would silently join lines somebody separated.
export function renderMarkdown(body: string): string {
  return marked.parse(body, { renderer, async: false, breaks: true, gfm: true }) as string;
}

// setMarkdown renders into el, replacing what was there.
//
// innerHTML is the point of the function; what makes it safe to call is the renderer above,
// so this is the ONLY place the console assigns rendered remark markup, and every caller goes
// through it rather than reaching for marked directly.
export function setMarkdown(el: HTMLElement, body: string): void {
  el.innerHTML = renderMarkdown(body);
}
