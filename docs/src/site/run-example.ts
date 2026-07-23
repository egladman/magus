// run-example.js - "Run ▶" button on opt-in Buzz code blocks.
//
// The markdown render tags a fence with data-magus-run="true" (via a <!-- magus-run -->
// author marker); this module finds those blocks and adds two action bars plus an
// output panel. Top bar: "Open in Playground ↗" (left, opens in a new tab, deep-linking
// the snippet into /playground/#source=<base64url>) and a copy-to-clipboard button
// (right) - runnable blocks skip code-copy.js's floating corner button (see there) and
// get this inline one instead. Bottom bar: the Run button (right-aligned), directly
// above where its output panel will land on click, which LAZY-LOADS the playground WASM
// (never on page load - the ~1.9 MB artifact would regress the perf work). Subsequent
// runs on the page reuse the cached module.
import { copyFeedback } from "../lib/clipboard.js";

// The playground WASM exposes window.buzz.* inside its Go main(), and wasm_exec.js
// defines window.Go; declare just the surface this module touches.
interface BuzzOp {
  target?: string;
  name: string;
  detail?: string;
  kind: string;
}
interface BuzzResult {
  ok: boolean;
  output?: string;
  trace?: BuzzOp[];
}
interface BuzzRuntime {
  evalBuzz(src: string): BuzzResult;
  evalBuzzWithRecorder(src: string): BuzzResult;
}
interface GoInstance {
  run(instance: WebAssembly.Instance): void;
  importObject: WebAssembly.Imports;
}
declare global {
  interface Window {
    buzz?: BuzzRuntime;
    Go: { new (): GoInstance };
  }
}

export function initRunExample(): void {
  const blocks = document.querySelectorAll("pre[data-magus-run]");
  if (!blocks.length) return;

  // Resolve the playground/ folder relative to this bundle so links work under
  // the /magus/ subpath and local preview alike.
  const ROOT = import.meta.url.replace(/main\.js(\?.*)?$/, "");

  // Lazy WASM loader. Returns a Promise that resolves once window.buzz is ready.
  let wasmPromise: Promise<void> | null = null;
  function ensureBuzz(): Promise<void> {
    if (window.buzz && typeof window.buzz.evalBuzz === "function") return Promise.resolve();
    if (wasmPromise) return wasmPromise;
    wasmPromise = new Promise<void>(function (resolve, reject) {
      // wasm_exec.js is a classic script that defines globalThis.Go; append it,
      // wait for load, then instantiate buzz.wasm exactly like playground.html.
      const s = document.createElement("script");
      s.src = ROOT + "playground/wasm_exec.js";
      s.onload = function () {
        try {
          const go = new window.Go();
          const loader = fetch(ROOT + "playground/buzz.wasm");
          const startWith = function (mod: WebAssembly.WebAssemblyInstantiatedSource): void {
            go.run(mod.instance);
            // The playground exposes window.buzz.evalBuzz inside main(); poll
            // briefly for it to appear before resolving (Go's main is async under
            // asyncify).
            const deadline = Date.now() + 5000;
            (function wait() {
              if (window.buzz && typeof window.buzz.evalBuzz === "function") return resolve();
              if (Date.now() > deadline) return reject(new Error("buzz.evalBuzz not ready"));
              setTimeout(wait, 30);
            })();
          };
          if (WebAssembly.instantiateStreaming) {
            WebAssembly.instantiateStreaming(loader, go.importObject).then(startWith).catch(reject);
          } else {
            loader
              .then(function (r) {
                return r.arrayBuffer();
              })
              .then(function (bs) {
                return WebAssembly.instantiate(bs, go.importObject);
              })
              .then(startWith)
              .catch(reject);
          }
        } catch (e) {
          reject(e);
        }
      };
      s.onerror = function () {
        reject(new Error("wasm_exec.js failed to load"));
      };
      document.head.appendChild(s);
    });
    // A transient load failure must not poison every later Run; drop the cache so the
    // next click retries. The caller still sees this attempt's rejection.
    wasmPromise.catch(() => {
      wasmPromise = null;
    });
    return wasmPromise;
  }

  const PLAY =
    '<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' +
    '<polygon points="5 3 19 12 5 21 5 3"></polygon></svg>';

  // Matches code-copy.js's icon exactly, so the runnable block's inline copy button
  // and every other code block's floating one read as the same control.
  const CLIPBOARD =
    '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' +
    '<rect x="9" y="9" width="13" height="13" rx="2"></rect>' +
    '<path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path></svg>';

  function base64url(text: string): string {
    // UTF-8 -> latin1 (unescape(encodeURIComponent)) -> btoa -> URL-safe alphabet.
    return btoa(unescape(encodeURIComponent(text)))
      .replace(/\+/g, "-")
      .replace(/\//g, "_")
      .replace(/=+$/, "");
  }

  // formatTrace renders an evalBuzzWithRecorder result as text lines, matching the
  // playground console's dry-run output: any printed output first, then one line
  // per planned op ("[target] name detail  kind · would run"), then a summary. On
  // failure it shows the diagnostic; with no ops it says nothing would run. The
  // "would run" / "planned, nothing executed" wording mirrors magus's own dry-run
  // idiom ("dry run - commands shown, not executed") so the playground reads as
  // genuine magus, not a bespoke format.
  function formatTrace(r: BuzzResult | null): string {
    if (!r) return "(no result)";
    if (!r.ok) return (r.output ? r.output + "\n" : "") + "dry run failed";
    const lines: string[] = [];
    if (r.output) lines.push(r.output);
    const trace = r.trace || [];
    for (let i = 0; i < trace.length; i++) {
      const op = trace[i];
      const tag = op.target ? "[" + op.target + "] " : "";
      const detail = op.detail ? " " + op.detail : "";
      lines.push(tag + op.name + detail + "  " + op.kind + " · would run");
    }
    const n = trace.length;
    lines.push("[dry] " + n + " step" + (n === 1 ? "" : "s") + " planned, nothing executed");
    return lines.join("\n");
  }

  blocks.forEach((pre) => {
    const code = pre.querySelector("code");
    if (!code) return;

    // Couple the controls to the code block itself: reuse the .code-block wrapper
    // (code-copy.js skips this pre, so normally none exists yet) and hang the top and
    // bottom bars off it so they read as part of the block. The output panel attaches
    // below the whole block on first run, so input and output stay a single unit.
    const parent = pre.parentElement;
    let block: HTMLElement;
    if (parent && parent.classList.contains("code-block")) {
      block = parent;
    } else {
      const w = document.createElement("div");
      w.className = "code-block";
      pre.parentNode?.insertBefore(w, pre);
      w.appendChild(pre);
      block = w;
    }
    block.classList.add("runnable");

    // Top bar: Open in Playground (left) + copy (right).
    const topBar = document.createElement("div");
    topBar.className = "runnable-bar runnable-bar-top";

    const openLink = document.createElement("a");
    openLink.className = "open-in-playground";
    openLink.href = ROOT + "playground/#source=" + base64url(code.textContent ?? "");
    openLink.target = "_blank";
    openLink.rel = "noopener";
    openLink.setAttribute("title", "Open this snippet in the playground (new tab)");
    openLink.setAttribute("data-tooltip", "Open in playground");
    openLink.append("Open in Playground ");
    const openArrow = document.createElement("span");
    openArrow.className = "oip-arrow";
    openArrow.setAttribute("aria-hidden", "true");
    openArrow.textContent = "↗";
    openLink.append(openArrow);
    topBar.appendChild(openLink);

    // Matches code-copy.js: no button at all where the Clipboard API is unavailable,
    // rather than an inert one.
    if (navigator.clipboard) {
      const copyBtn = document.createElement("button");
      copyBtn.type = "button";
      copyBtn.className = "runnable-copy";
      copyBtn.setAttribute("aria-label", "Copy code to clipboard");
      copyBtn.setAttribute("title", "Copy code to clipboard");
      copyBtn.setAttribute("data-tooltip", "Copy code");
      copyBtn.innerHTML = CLIPBOARD;
      topBar.appendChild(copyBtn);
      copyFeedback({
        el: copyBtn,
        getText: () => code.textContent,
        restIcon: CLIPBOARD,
        restLabel: "Copy code to clipboard",
        doneLabel: "Copied",
        failLabel: "Copy failed",
      });
    }

    block.insertBefore(topBar, pre);

    // Bottom bar: Run alone, right-aligned, sitting directly above where its
    // output panel lands.
    const bottomBar = document.createElement("div");
    bottomBar.className = "runnable-bar runnable-bar-bottom";

    const runBtn = document.createElement("button");
    runBtn.type = "button";
    runBtn.className = "run-example";
    runBtn.innerHTML = PLAY + "<span>Run</span>";
    runBtn.setAttribute("aria-label", "Run this Buzz snippet");
    runBtn.setAttribute("title", "Run this Buzz snippet");
    runBtn.setAttribute("data-tooltip", "Run this snippet");
    bottomBar.appendChild(runBtn);

    pre.insertAdjacentElement("afterend", bottomBar);

    // Output panel inserted after the whole block on first run.
    let out: HTMLPreElement | null = null;
    function panel(): HTMLPreElement {
      if (out) return out;
      out = document.createElement("pre");
      out.className = "runnable-output";
      block.parentNode?.insertBefore(out, block.nextSibling);
      return out;
    }

    runBtn.addEventListener("click", () => {
      runBtn.disabled = true;
      const span = runBtn.querySelector("span");
      const oldLabel = span?.textContent ?? "";
      if (span) span.textContent = "Running…";
      // Spell examples opt into the dry-run recorder (data-magus-recorder): their
      // targets fork tools, so evalBuzz can't run them, but evalBuzzWithRecorder
      // reports the tool invocations they WOULD trigger as a trace. Module
      // examples stay on the plain evalBuzz path (print output).
      const recorder = pre.hasAttribute("data-magus-recorder");
      ensureBuzz()
        .then(() => {
          const buzz = window.buzz;
          if (!buzz) throw new Error("buzz.evalBuzz not ready");
          const pnl = panel();
          const src = code.textContent ?? "";
          if (recorder) {
            const r = buzz.evalBuzzWithRecorder(src);
            pnl.textContent = formatTrace(r);
            pnl.classList.toggle("failed", !(r && r.ok));
          } else {
            const r = buzz.evalBuzz(src);
            pnl.textContent = r && r.output ? r.output : "(no output)";
            pnl.classList.toggle("failed", !(r && r.ok));
          }
        })
        .catch((e) => {
          const pnl = panel();
          pnl.textContent =
            "Failed to load the Buzz runtime: " + (e instanceof Error ? e.message : String(e));
          pnl.classList.add("failed");
        })
        .finally(() => {
          runBtn.disabled = false;
          if (span) span.textContent = oldLabel;
        });
    });
  });
}
