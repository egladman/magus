// run-example.js - "Run ▶" button on opt-in Buzz code blocks.
//
// The markdown render tags a fence with data-runnable="true" (via a <!-- run -->
// author marker); this module finds those blocks, adds a Run button + an output
// panel, and on click LAZY-LOADS the playground WASM (never on page load - the
// ~1.9 MB artifact would regress the perf work). Subsequent runs on the page
// reuse the cached module. Also adds an "Open in Playground →" link that deep-
// links the snippet into /playground.html#source=<base64url> (WS M's loader).

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

  blocks.forEach(function (pre) {
    var code = pre.querySelector("code");
    if (!code) return;

    // Toolbar row above the block: Run + Open-in-Playground.
    var bar = document.createElement("div");
    bar.className = "runnable-toolbar";

    var runBtn = document.createElement("button");
    runBtn.type = "button";
    runBtn.className = "run-example";
    runBtn.innerHTML = PLAY + '<span>Run</span>';
    runBtn.setAttribute("aria-label", "Run this Buzz snippet");
    bar.appendChild(runBtn);

    var openLink = document.createElement("a");
    openLink.className = "open-in-playground";
    openLink.textContent = "Open in Playground →";
    openLink.href = ROOT + "playground.html#source=" + base64url(code.textContent);
    openLink.setAttribute("title", "Open this snippet in the playground");
    bar.appendChild(openLink);

    pre.parentNode.insertBefore(bar, pre);

    // Output panel inserted after the block on first successful run.
    var out = null;
    function panel() {
      if (out) return out;
      out = document.createElement("pre");
      out.className = "runnable-output";
      pre.parentNode.insertBefore(out, pre.nextSibling);
      return out;
    }

    runBtn.addEventListener("click", function () {
      runBtn.disabled = true;
      var oldLabel = runBtn.querySelector("span").textContent;
      runBtn.querySelector("span").textContent = "Running…";
      ensureBuzz().then(function () {
        var r = window.buzz.evalBuzz(code.textContent);
        var pnl = panel();
        pnl.textContent = (r && r.output) ? r.output : "(no output)";
        pnl.classList.toggle("failed", !(r && r.ok));
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
