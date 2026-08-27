// screenshot.mjs - capture one console surface, driven over the Chrome DevTools Protocol.
//
// Why CDP and not `chrome --headless --screenshot`, which is simpler and is what this used to be:
// the flag form cannot express a phone. Two hard limits, both measured rather than assumed.
//
//   - `--window-size=375,812` renders at 500px wide. Headless clamps the window to a 500px minimum
//     on macOS and reports 500 to the page, silently. 320/375/400/450 all come out 500.
//   - The flag form is always `pointer: fine`. `--touch-events=enabled` does not change it. So the
//     console's touch-target sizing - which is keyed on `(pointer: coarse)`, not on width - is
//     invisible to it, and a "mobile" screenshot taken that way would show a phone-width layout
//     wearing desktop-sized controls, i.e. a picture of something that does not exist.
//
// Emulation.setDeviceMetricsOverride with mobile:true fixes both at once. Verified in this exact
// setup: the page reports { coarse: true, w: 375, maxTouchPoints: 5 }.
//
// No npm dependency: Node ships a global WebSocket (>=22), which is the entire reason this can be a
// hack script rather than a puppeteer install. The top of screenshots.sh explains why a browser
// driver is a dependency this repo does not want.
//
//   node hacks/screenshot.mjs <chrome> <url> <out.png> <width> <height> <scale> <mobile:0|1> [keys]
//
// `keys` is a comma-separated list of keys to press once the surface has settled, so a picture can
// show a state that only a keystroke reaches - a send box, an overview. A surface reachable only by
// typing is the one a page most needs a picture of, and hand-capturing it puts a screenshot in the
// repo that nothing can reproduce.

import { spawn } from "node:child_process";
import { writeFileSync } from "node:fs";

const [chrome, url, out, w, h, scale, mobileFlag, keys] = process.argv.slice(2);
const width = Number(w);
const height = Number(h);
const deviceScaleFactor = Number(scale);
const mobile = mobileFlag === "1";

// Named keys the shots table uses. Chrome needs the virtual key code for these; there is no
// lookup in CDP, so the list grows as a picture needs one.
const KEY_CODES = { Escape: 27, Enter: 13, Tab: 9 };

// A per-process port so two captures can never collide on a shared debugging socket.
const port = 9222 + (process.pid % 500);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// --force-prefers-reduced-motion is load-bearing here for the same reason screenshots.sh gives it
// to the flag form: a surface with a perpetual animation loop never settles otherwise.
const proc = spawn(
  chrome,
  [
    "--headless=new",
    "--disable-gpu",
    "--no-sandbox",
    "--hide-scrollbars",
    "--force-prefers-reduced-motion",
    `--remote-debugging-port=${port}`,
    "about:blank",
  ],
  { stdio: "ignore" },
);

async function pageSocket() {
  for (let i = 0; i < 100; i++) {
    try {
      const res = await fetch(`http://127.0.0.1:${port}/json/list`);
      const page = (await res.json()).find((t) => t.type === "page");
      if (page?.webSocketDebuggerUrl) return page.webSocketDebuggerUrl;
    } catch {
      // devtools is not listening yet; that is the normal first few hundred ms
    }
    await sleep(100);
  }
  throw new Error("screenshot: chrome devtools never came up");
}

let ws;
try {
  ws = new WebSocket(await pageSocket());
  await new Promise((resolve, reject) => {
    ws.onopen = resolve;
    ws.onerror = () => reject(new Error("screenshot: devtools socket refused"));
  });

  let seq = 0;
  const pending = new Map();
  const seen = new Set();
  ws.onmessage = (m) => {
    const msg = JSON.parse(m.data);
    if (msg.id && pending.has(msg.id)) {
      pending.get(msg.id)(msg.result);
      pending.delete(msg.id);
    } else if (msg.method) {
      seen.add(msg.method);
    }
  };
  const send = (method, params = {}) =>
    new Promise((resolve) => {
      const id = ++seq;
      pending.set(id, resolve);
      ws.send(JSON.stringify({ id, method, params }));
    });

  await send("Page.enable");
  // The console's theme defaults to "auto" - the OS preference - and headless Chrome inherits the
  // machine's appearance, so the committed gallery would flip theme with whoever ran this last.
  // Pinned light to match the gallery it feeds.
  await send("Emulation.setEmulatedMedia", {
    features: [{ name: "prefers-color-scheme", value: "light" }],
  });
  await send("Emulation.setDeviceMetricsOverride", { width, height, deviceScaleFactor, mobile });
  if (mobile) {
    await send("Emulation.setTouchEmulationEnabled", { enabled: true, maxTouchPoints: 5 });
    await send("Emulation.setEmitTouchEventsForMouse", { enabled: true, configuration: "mobile" });
  }
  await send("Page.navigate", { url });
  for (let i = 0; i < 120 && !seen.has("Page.loadEventFired"); i++) await sleep(100);
  // The demo surfaces reveal incrementally (the log viewer streams its fixture in), so load is the
  // start of the picture rather than the end of it.
  await sleep(3000);

  for (const key of (keys ?? "").split(",").filter(Boolean)) {
    // A printable key needs `text` for the surface to read it as typing; a named one must not
    // carry text at all, or the page receives a character rather than the key.
    const printable = key.length === 1;
    const base = printable ? { text: key, key } : { key, windowsVirtualKeyCode: KEY_CODES[key] };
    if (!printable && base.windowsVirtualKeyCode === undefined) {
      throw new Error(`screenshot: no key code for ${key}`);
    }
    await send("Input.dispatchKeyEvent", { type: "keyDown", ...base });
    await send("Input.dispatchKeyEvent", { type: "keyUp", ...base });
    await sleep(600);
  }

  const shot = await send("Page.captureScreenshot", { format: "png" });
  if (!shot?.data) throw new Error("screenshot: chrome returned no image data");
  writeFileSync(out, Buffer.from(shot.data, "base64"));
} finally {
  ws?.close();
  proc.kill();
}
