// run-example.js - "Run ▶" button on opt-in Buzz code blocks.
//
// The markdown render tags a fence with data-runnable="true" (via a <!-- run -->
// author marker); this module finds those blocks, adds a Run button + an output
// panel, and on click LAZY-LOADS the playground WASM (never on page load - the
// ~1.9 MB artifact would regress the perf work). Subsequent runs on the page
// reuse the cached module. Also adds an "Open in Playground ↗" link (opens in a
// new tab) that deep-links the snippet into /playground/#source=<base64url>.

(function () {
  var blocks = document.querySelectorAll("pre[data-runnable]");
  if (!blocks.length) return;

  // Resolve the playground/ folder relative to this bundle so links work under
  // the /magus/ subpath and local preview alike.
  var ROOT = import.meta.url.replace(/main\.js(\?.*)?$/, "");

  // Lazy WASM loader. Returns a Promise that resolves once window.buzz is ready.
  var wasmPromise = null;
  function ensureBuzz() {
    if (window.buzz && window.buzz.evalBuzz) return Promise.resolve();
    if (wasmPromise) return wasmPromise;
    wasmPromise = new Promise(function (resolve, reject) {
      // wasm_exec.js is a classic script that defines globalThis.Go; append it,
      // wait for load, then instantiate buzz.wasm exactly like playground.html.
      var s = document.createElement("script");
      s.src = ROOT + "playground/wasm_exec.js";
      s.onload = function () {
        try {
          var go = new window.Go();
          var loader = fetch(ROOT + "playground/buzz.wasm");
          var startWith = function (mod) {
            go.run(mod.instance);
            // The playground exposes window.buzz.evalBuzz inside main(); poll
            // briefly for it to appear before resolving (Go's main is async under
            // asyncify).
            var deadline = Date.now() + 5000;
            (function wait() {
              if (window.buzz && window.buzz.evalBuzz) return resolve();
              if (Date.now() > deadline) return reject(new Error("buzz.evalBuzz not ready"));
              setTimeout(wait, 30);
            })();
          };
          if (WebAssembly.instantiateStreaming) {
            WebAssembly.instantiateStreaming(loader, go.importObject).then(startWith).catch(reject);
          } else {
            loader.then(function (r) { return r.arrayBuffer(); })
              .then(function (bs) { return WebAssembly.instantiate(bs, go.importObject); })
              .then(startWith).catch(reject);
          }
        } catch (e) { reject(e); }
      };
      s.onerror = function () { reject(new Error("wasm_exec.js failed to load")); };
      document.head.appendChild(s);
    });
    return wasmPromise;
  }

  var PLAY =
    '<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' +
    '<polygon points="5 3 19 12 5 21 5 3"></polygon></svg>';

  function base64url(text) {
    // UTF-8 -> latin1 (unescape(encodeURIComponent)) -> btoa -> URL-safe alphabet.
    return btoa(unescape(encodeURIComponent(text)))
      .replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
  }

  // formatTrace renders an evalBuzzWithRecorder result as text lines, matching the
  // playground console's dry-run output: any printed output first, then one line
  // per recorded op ("[target] name detail  kind · recorded"), then a summary. On
  // failure it shows the diagnostic; with no ops it says nothing would run.
  function formatTrace(r) {
    if (!r) return "(no result)";
    if (!r.ok) return (r.output ? r.output + "\n" : "") + "dry-run failed";
    var lines = [];
    if (r.output) lines.push(r.output);
    var trace = r.trace || [];
    for (var i = 0; i < trace.length; i++) {
      var op = trace[i];
      var tag = op.target ? "[" + op.target + "] " : "";
      var detail = op.detail ? " " + op.detail : "";
      lines.push(tag + op.name + detail + "  " + op.kind + " · recorded");
    }
    var n = trace.length;
    lines.push("[pass] dry-run: " + n + " step" + (n === 1 ? "" : "s") +
      " recorded, nothing executed");
    return lines.join("\n");
  }

  blocks.forEach(function (pre) {
    var code = pre.querySelector("code");
    if (!code) return;

    // Couple the controls to the code block itself: reuse the .code-block wrapper
    // code-copy.js already added (or create one), then hang an action bar off the
    // BOTTOM of that wrapper so Run + Open-in-Playground read as part of the block
    // rather than a row floating above it. The output panel attaches directly below
    // the same block, so the whole thing is one visually-connected unit.
    var block = pre.parentElement && pre.parentElement.classList.contains("code-block")
      ? pre.parentElement
      : (function () {
          var w = document.createElement("div");
          w.className = "code-block";
          pre.parentNode.insertBefore(w, pre);
          w.appendChild(pre);
          return w;
        })();
    block.classList.add("runnable");

    var bar = document.createElement("div");
    bar.className = "runnable-bar";

    var runBtn = document.createElement("button");
    runBtn.type = "button";
    runBtn.className = "run-example";
    runBtn.innerHTML = PLAY + '<span>Run</span>';
    runBtn.setAttribute("aria-label", "Run this Buzz snippet");
    runBtn.setAttribute("title", "Run this Buzz snippet");
    runBtn.setAttribute("data-tooltip", "Run this snippet");
    bar.appendChild(runBtn);

    var openLink = document.createElement("a");
    openLink.className = "open-in-playground";
    openLink.href = ROOT + "playground/#source=" + base64url(code.textContent);
    openLink.target = "_blank";
    openLink.rel = "noopener";
    openLink.setAttribute("title", "Open this snippet in the playground (new tab)");
    openLink.setAttribute("data-tooltip", "Open in playground");
    openLink.append("Open in Playground ");
    var openArrow = document.createElement("span");
    openArrow.className = "oip-arrow";
    openArrow.setAttribute("aria-hidden", "true");
    openArrow.textContent = "↗";
    openLink.append(openArrow);
    bar.appendChild(openLink);

    block.appendChild(bar);

    // Output panel inserted after the whole block on first run.
    var out = null;
    function panel() {
      if (out) return out;
      out = document.createElement("pre");
      out.className = "runnable-output";
      block.parentNode.insertBefore(out, block.nextSibling);
      return out;
    }

    runBtn.addEventListener("click", function () {
      runBtn.disabled = true;
      var oldLabel = runBtn.querySelector("span").textContent;
      runBtn.querySelector("span").textContent = "Running…";
      // Spell examples opt into the dry-run recorder (data-recorder): their
      // targets fork tools, so evalBuzz can't run them, but evalBuzzWithRecorder
      // reports the tool invocations they WOULD trigger as a trace. Module
      // examples stay on the plain evalBuzz path (print output).
      var recorder = pre.hasAttribute("data-recorder");
      ensureBuzz().then(function () {
        var pnl = panel();
        if (recorder) {
          var r = window.buzz.evalBuzzWithRecorder(code.textContent);
          pnl.textContent = formatTrace(r);
          pnl.classList.toggle("failed", !(r && r.ok));
        } else {
          var r = window.buzz.evalBuzz(code.textContent);
          pnl.textContent = (r && r.output) ? r.output : "(no output)";
          pnl.classList.toggle("failed", !(r && r.ok));
        }
      }).catch(function (e) {
        var pnl = panel();
        pnl.textContent = "Failed to load the Buzz runtime: " + e.message;
        pnl.classList.add("failed");
      }).then(function () {
        runBtn.disabled = false;
        runBtn.querySelector("span").textContent = oldLabel;
      });
    });
  });
})();
