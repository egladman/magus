// buzz-console.ts - a hidden Buzz REPL, on every page, for anyone who types "buzz".
//
// Undocumented on purpose: it is not in the "?" shortcut list and nothing on the page
// hints at it. Type the language's name outside a text field and a small console opens
// with a live interpreter behind it - the same wasm the playground and the Run buttons
// use, reached through lib/buzz-runtime so a page never fetches it twice.
//
// The cost of carrying it on every page is this file's bundled bytes plus one keydown
// listener; the multi-megabyte interpreter is fetched only once someone actually opens
// the console, so a reader who never finds it pays nothing for it.
import { type BuzzResult, type BuzzRuntime, ensureBuzz } from "../lib/buzz-runtime.js";

// The trigger. Typed as ordinary text, so it deliberately reads as a thing you might do
// by accident on a site about Buzz - which is the discovery path.
const WORD = "buzz";

export function initBuzzConsole(): void {
  if (typeof document === "undefined") return;

  // Built on first open, like keyboard-help's overlay: a page nobody triggers it on
  // never constructs any of this.
  let dialog: HTMLDialogElement | null = null;
  let log: HTMLElement | null = null;
  let input: HTMLInputElement | null = null;
  // Submitted lines, oldest first, walked with Up/Down. cursor === history.length means
  // "on the fresh line", which is what Down returns to.
  const history: string[] = [];
  let cursor = 0;

  // say appends one line to the log. Class-tagged rather than styled inline so the
  // stylesheet owns the look; textContent throughout, so a result that happens to
  // contain markup renders as the text it is.
  function say(text: string, cls?: string): void {
    if (!log) return;
    const line = document.createElement("div");
    if (cls) line.className = cls;
    line.textContent = text;
    log.appendChild(line);
    log.scrollTop = log.scrollHeight;
  }

  // report renders one evaluation: whatever the program printed, then its trailing
  // value. A program with no trailing `return` yields "null", which is noise in a REPL,
  // so it is dropped rather than shown.
  function report(buzz: BuzzRuntime, src: string, r: BuzzResult): void {
    if (r.output) say(r.output.replace(/\n+$/, ""), "buzz-console-out");
    if (r.ok) {
      if (r.result && r.result !== "null") say(r.result, "buzz-console-value");
      return;
    }
    // evalBuzz reports failure without saying why; diagnostics() is a pure function of
    // the same source, so ask it for the message rather than printing a bare "failed".
    const diags = buzz.diagnostics(src);
    say(diags.length ? diags[0].msg : "evaluation failed", "buzz-console-fail");
  }

  function run(line: string): void {
    if (!input) return;
    say("> " + line, "buzz-console-echo");
    history.push(line);
    cursor = history.length;
    input.value = "";
    input.disabled = true;
    ensureBuzz()
      .then((buzz) => {
        // A REPL user types an expression; Buzz evaluates a program, and only a
        // trailing `return` produces a value. Wrap a single semicolon-free line so
        // `1 + 2` answers 3, and fall back to running it verbatim when the wrapped
        // form does not compile - which is what `var x = 1` (no semicolon) needs.
        const bare = line.indexOf(";") === -1;
        if (bare) {
          const wrapped = buzz.evalBuzz("return " + line + ";");
          if (wrapped.ok) return report(buzz, "return " + line + ";", wrapped);
        }
        report(buzz, line, buzz.evalBuzz(line));
      })
      .catch((e) => {
        say(
          "could not load the interpreter: " + (e instanceof Error ? e.message : String(e)),
          "buzz-console-fail",
        );
      })
      .finally(() => {
        if (!input) return;
        input.disabled = false;
        input.focus();
      });
  }

  function build(): HTMLDialogElement {
    const d = document.createElement("dialog");
    d.className = "buzz-console";
    d.innerHTML =
      "<article>" +
      "<header><h2>buzz</h2>" +
      '<button type="button" aria-label="Close" class="shortcut-close">&times;</button></header>' +
      '<div class="buzz-console-log" role="log" aria-live="polite"></div>' +
      // A <div>, not a <form>: the prompt is one line handled entirely by the keydown
      // below, and a real form here would only add a submission the site's CSP
      // (form-action 'none') exists to forbid.
      '<div class="buzz-console-form">' +
      '<label class="buzz-console-prompt" for="buzz-console-input">&gt;</label>' +
      '<input id="buzz-console-input" type="text" autocomplete="off" autocapitalize="off" ' +
      'spellcheck="false" aria-label="Buzz expression">' +
      "</div>" +
      "</article>";
    document.body.appendChild(d);

    log = d.querySelector(".buzz-console-log");
    input = d.querySelector("input");
    d.querySelector(".shortcut-close")?.addEventListener("click", () => d.close());
    // Click-outside closes: a click whose target is the dialog itself (not its inner
    // article) landed on the full-viewport backdrop.
    d.addEventListener("click", (e) => {
      if (e.target === d) d.close();
    });
    input?.addEventListener("keydown", (e: KeyboardEvent) => {
      if (!input) return;
      // <dialog> closes itself on Escape, but only as a default action on a real key
      // press; handling it here means the banner's promise holds however the key
      // arrives, and closing an already-closed dialog is a no-op.
      if (e.key === "Escape") {
        d.close();
        return;
      }
      if (e.key === "Enter") {
        e.preventDefault();
        const line = input.value.trim();
        if (line) run(line);
        return;
      }
      if (e.key !== "ArrowUp" && e.key !== "ArrowDown") return;
      if (!history.length) return;
      // Up walks back through submitted lines, Down walks forward; one step past the
      // newest lands on the empty line the reader was typing.
      e.preventDefault();
      cursor = e.key === "ArrowUp" ? Math.max(0, cursor - 1) : Math.min(history.length, cursor + 1);
      input.value = cursor === history.length ? "" : history[cursor];
    });

    // Two lines only: what to press, and one line worth pasting. Every line evaluates as
    // its own program - there is no session state between them - so the second example
    // shows the whole-program shape rather than a bare statement.
    say("A Buzz interpreter. Enter runs a line, Esc closes.", "buzz-console-note");
    say('Try:  "magus".len()   or   import "std"; std\\print("hello");', "buzz-console-note");
    dialog = d;
    return d;
  }

  function open(): void {
    const d = dialog ?? build();
    if (d.open) return;
    if (typeof d.showModal === "function") d.showModal();
    else d.setAttribute("open", "");
    input?.focus();
    // Start the download the moment the console is visible rather than on the first
    // Enter: the reader is now certain to want it, and the fetch overlaps the seconds
    // they spend typing. Failures surface on that first run, so ignore them here.
    ensureBuzz().catch(() => {});
  }

  // The trigger buffer: the last WORD.length printable keys typed outside a text field.
  let typed = "";
  document.addEventListener("keydown", (e: KeyboardEvent) => {
    if (e.metaKey || e.ctrlKey || e.altKey) return;
    // Never while the reader is typing into something - including this console's own
    // input, so the word is inert once the console is open.
    const el = e.target as HTMLElement | null;
    if (/^(INPUT|TEXTAREA|SELECT)$/.test(el?.tagName ?? "") || el?.isContentEditable) return;
    if (e.key.length !== 1) return;
    typed = (typed + e.key.toLowerCase()).slice(-WORD.length);
    if (typed !== WORD) return;
    typed = "";
    // Swallow the keystroke that completed the word: open() focuses the prompt, and
    // without this the final "z" arrives in it as the first character typed.
    e.preventDefault();
    open();
  });
}
