// hero-terminal.ts - turns the landing page's terminal frame into a real one.
//
// Progressive enhancement, strictly. The server renders a static transcript inside
// .term[data-hero-terminal]; that transcript IS the no-JS experience and stays
// exactly as rendered. This module appends a prompt line below it and starts
// accepting input. If the bundle never loads, the page is unchanged.
//
// What actually runs: Buzz, through the playground's WASM (window.buzz.evalBuzz).
// That is a real interpreter and a real evaluation - a magusfile is Buzz, so typing
// Buzz here is the same language the config examples further down the page are
// written in.
//
// What does NOT run: magus itself. There is no workspace, no filesystem, no git and
// no subprocesses in a browser tab, so `magus ...` cannot be executed. Rather than
// fake a result, those commands answer by saying so and pointing at the real CLI.
// A page whose headline is "No magic. Just commands." cannot afford a puppet shell.
import { ensureBuzz, buzzReady, warmBuzz } from "./buzz-runtime.js";

const HISTORY_MAX = 50;

// Sitting in the prompt when the page loads. It reads as a screenshot of a shell
// with the next command already typed, and it means the only thing between a
// visitor and finding out this box is live is the Enter key. Nothing labels it as
// interactive - working that out is the whole joke.
const PRETYPED = "magus affected ci --dry-run";

const HELP = [
  "  help    this",
  "  clear   clear the screen",
  "",
  "  anything else is evaluated as Buzz, the language a magusfile is",
  "  written in. Try:  import \"std\"; std\\print(1 + 1);",
].join("\n");

// The reply to `magus <anything>`, and the reason this shell is not a liar: magus
// needs a workspace, a filesystem and subprocesses, none of which exist in a tab.
const NO_MAGUS = [
  "magus needs a workspace, a filesystem and subprocesses, so it cannot run",
  "in a browser tab. This box runs Buzz - the language a magusfile is written",
  "in. Install magus to run that command for real.",
].join("\n");

export function initHeroTerminal(): void {
  const term = document.querySelector<HTMLElement>("[data-hero-terminal]");
  if (!term) return;
  const body = term.querySelector<HTMLElement>(".term-body");
  if (!body) return;

  // Start fetching the runtime now, long before anyone types. The service worker
  // precaches buzz.wasm, so this is usually a Cache Storage read rather than a
  // download, and by the time someone has clicked in and typed a line it is ready.
  // The whole point is that the load is never something the visitor waits on.
  warmBuzz();

  // The transcript's block cursor moves onto the live prompt rather than being
  // dropped. The native caret only shows once the field has focus, so without it
  // nothing says the terminal takes typing at all.
  const cursor = body.querySelector(".term-cursor");

  // One contenteditable-free input: a bare <input> keeps mobile keyboards, IME,
  // and autofill behaving normally, and it is styled to sit flush in the transcript
  // so it reads as the next line rather than as a form field.
  const line = document.createElement("div");
  line.className = "term-input-line";
  line.innerHTML =
    '<label class="term-prompt" for="hero-term-input">$</label>' +
    '<input id="hero-term-input" class="term-input" type="text" autocomplete="off" ' +
    'autocapitalize="off" autocorrect="off" spellcheck="false" placeholder=" " ' +
    'aria-label="Terminal input">';
  if (cursor) line.appendChild(cursor);
  body.appendChild(line);
  const input = line.querySelector<HTMLInputElement>(".term-input");
  if (!input) return;

  // A second warm signal, earlier than any keystroke: pointing at the box is intent.
  term.addEventListener("pointerenter", warmBuzz, { once: true });
  term.addEventListener("focusin", warmBuzz, { once: true });
  // Clicking anywhere in the frame focuses the prompt, the way a terminal behaves -
  // but only when the click was not a text selection, or copying output would be
  // impossible.
  term.addEventListener("click", function () {
    if ((window.getSelection()?.toString() ?? "") === "") input.focus();
  });

  const history: string[] = [];
  let historyAt = 0;

  function write(text: string, cls?: string): void {
    const el = document.createElement("span");
    if (cls) el.className = cls;
    el.textContent = text + "\n";
    body!.insertBefore(el, line);
  }

  function echo(cmd: string): void {
    const el = document.createElement("span");
    el.innerHTML = '<span class="term-prompt">$</span> <span class="term-cmd"></span>\n';
    el.querySelector(".term-cmd")!.textContent = cmd;
    body!.insertBefore(el, line);
  }

  input.value = PRETYPED;

  // Shrink-wrap the field to its own text so the block cursor, which is the next
  // flex item, sits immediately after it. Exact because the line is monospace.
  // Without this the input is flex: 1 and the cursor is pushed to the far right.
  function sizeInput(): void {
    input!.style.width = Math.max(input!.value.length, 1) + "ch";
  }
  sizeInput();
  input.addEventListener("input", sizeInput);

  function run(src: string): void {
    const buzz = window.buzz;
    if (!buzz) {
      write("the interpreter did not load; try the playground instead", "term-err");
      return;
    }
    let res;
    try {
      res = buzz.evalBuzz(src);
    } catch (e) {
      write(String(e), "term-err");
      return;
    }
    const out = (res.output ?? "").replace(/\n+$/, "");
    if (out !== "") write(out, res.ok ? undefined : "term-err");
    if (res.ok) return;
    // The interpreter reports a message and a 1-based position; show both. A shell
    // that can only say "error" teaches nobody anything, which is the opposite of
    // the point being made everywhere else on this page.
    const d = res.diag;
    if (d && d.msg) write(d.line > 0 ? d.line + ":" + d.col + ": " + d.msg : d.msg, "term-err");
    else if (out === "") write("error", "term-err");
  }

  function submit(raw: string): void {
    const cmd = raw.trim();
    echo(raw);
    if (cmd !== "") {
      history.push(cmd);
      if (history.length > HISTORY_MAX) history.shift();
    }
    historyAt = history.length;

    if (cmd === "") return;
    if (cmd === "clear") {
      // Keep the prompt; drop everything above it.
      while (body!.firstChild && body!.firstChild !== line) body!.removeChild(body!.firstChild);
      return;
    }
    if (cmd === "help") {
      write(HELP, "term-muted");
      return;
    }
    // `magus ...` is answered, not simulated. See NO_MAGUS.
    if (cmd.split(/\s+/)[0] === "magus") {
      write(NO_MAGUS, "term-muted");
      return;
    }

    if (buzzReady()) return run(cmd);
    // Not warm yet: await the same shared promise. Reached only if someone types
    // faster than the idle-time warm, so no spinner - a progress indicator here
    // would advertise a wait that is almost always already over.
    ensureBuzz().then(
      function () {
        run(cmd);
      },
      function () {
        write("the interpreter did not load; try the playground instead", "term-err");
      },
    );
  }

  input.addEventListener("keydown", function (ev) {
    if (ev.key === "Enter") {
      ev.preventDefault();
      const v = input.value;
      input.value = "";
      submit(v);
      term.scrollTop = term.scrollHeight;
      body!.scrollTop = body!.scrollHeight;
      return;
    }
    if (ev.key === "ArrowUp") {
      if (historyAt > 0) {
        historyAt -= 1;
        input.value = history[historyAt];
        ev.preventDefault();
      }
      return;
    }
    if (ev.key === "ArrowDown") {
      if (historyAt < history.length - 1) {
        historyAt += 1;
        input.value = history[historyAt];
      } else {
        historyAt = history.length;
        input.value = "";
      }
      ev.preventDefault();
      return;
    }
    if (ev.key === "l" && ev.ctrlKey) {
      ev.preventDefault();
      submit("clear");
    }
  });

  // No banner, no hint bar, no "try this" affordance: the page never announces
  // that this box is live. It looks like a screenshot of a shell with one command
  // already typed, and anyone who presses Enter finds out.
  term.setAttribute("data-hero-terminal", "live");
  // The static transcript was labelled as an image for assistive tech, which is
  // right for a fixed picture of output and wrong for a live control.
  term.removeAttribute("role");
  term.removeAttribute("aria-label");
}
