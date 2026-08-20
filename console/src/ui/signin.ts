// signin.ts - one screen: who you are, and which workspace this window follows.
//
// ONE SCREEN, NOT TWO, and the order is forced rather than chosen. The workspace list comes from
// GetStatus, which is authenticated - an unauthenticated caller gets 401, and the tokenless /readyz
// discloses only a COUNT ("1 loaded"), never a path. So there is no version of this where you pick a
// workspace before you are known: the chooser cannot populate until the credential works. Rather than
// two pages that hide that dependency, this is one page whose second half fills in when the first
// half succeeds.
//
// WHY SHOW AUTH AT ALL when the link already carries the token. Because it does carry it: the daemon
// mints a URL with #token=, the console swallows it, scrubs the fragment, and silently trades the
// operator credential for a console-scoped one. Everything works and nothing ever says that a
// credential changed hands - so nobody learns that the link IS the credential, or that forwarding it
// forwards access. Showing the filled-in field is not friction for its own sake; it is the only
// moment the system admits what it just did.
//
// The workspace is PRESELECTED, never auto-applied. A remembered pick fills the answer in so the
// common case is one click, but the click still happens - an inherited scope you never chose is
// exactly the "where did my work go" failure this screen exists to prevent.
import { getLiveToken, hasScopedToken, resolveDaemonHost } from "../lib/daemon";
import { ALL_WORKSPACES, setWorkspaceScope, shortName, workspaceScope } from "../lib/scope";

const ASKED_KEY = "magus:workspace-asked";
const LAST_KEY = "magus:workspace-last";

function alreadyAsked(): boolean {
  try {
    return sessionStorage.getItem(ASKED_KEY) === "1";
  } catch {
    return false;
  }
}

function markAsked(): void {
  try {
    sessionStorage.setItem(ASKED_KEY, "1");
  } catch {
    // Not durable; the in-memory guard still prevents a re-ask this session.
  }
}

function lastPick(): string {
  try {
    return localStorage.getItem(LAST_KEY) ?? "";
  } catch {
    return "";
  }
}

function rememberPick(root: string): void {
  try {
    if (root) localStorage.setItem(LAST_KEY, root);
    else localStorage.removeItem(LAST_KEY);
  } catch {
    // A forgotten preselection costs one extra click next time.
  }
}

// A credential is shown as evidence, never in full: enough to confirm one arrived and to tell two
// apart, never enough to read over a shoulder or lift from a screenshot.
function tokenEvidence(token: string | null): string {
  if (!token) return "No credential - this daemon is open on loopback.";
  const tail = token.length > 4 ? token.slice(-4) : token;
  return "From your link, ending " + tail + (hasScopedToken() ? " (console-scoped)" : "");
}

let asking = false;

// maybeAskWorkspace shows the screen when this browser tab has not chosen yet and the choice carries
// information. Safe to call on every status tick.
export function maybeAskWorkspace(roots: readonly string[]): void {
  if (asking || alreadyAsked()) return;
  if (roots.length < 2) return; // one workspace is one possible answer
  if (workspaceScope() !== ALL_WORKSPACES) return; // this tab already knows where it is
  ask(roots);
}

function ask(roots: readonly string[]): void {
  asking = true;
  const backdrop = document.createElement("div");
  backdrop.className = "pf-v6-c-backdrop console-shell-signin";
  const bullseye = document.createElement("div");
  bullseye.className = "pf-v6-l-bullseye";
  const box = document.createElement("div");
  box.className = "pf-v6-c-modal-box pf-m-sm";
  box.setAttribute("role", "dialog");
  box.setAttribute("aria-modal", "true");
  box.setAttribute("aria-labelledby", "console-signin-title");

  const header = document.createElement("header");
  header.className = "pf-v6-c-modal-box__header";
  const title = document.createElement("h1");
  title.className = "pf-v6-c-modal-box__title";
  title.id = "console-signin-title";
  title.textContent = "Connect";
  header.append(title);

  const body = document.createElement("div");
  body.className = "pf-v6-c-modal-box__body";

  // --- who you are, already filled in -----------------------------------------------------------
  const connWrap = document.createElement("div");
  connWrap.className = "console-shell-signin__section";
  const connLabel = document.createElement("span");
  connLabel.className = "console-shell-signin__label";
  connLabel.textContent = "Daemon";
  const connHost = document.createElement("code");
  connHost.className = "console-shell-signin__value";
  connHost.textContent = resolveDaemonHost() ?? "not configured";
  const credLabel = document.createElement("span");
  credLabel.className = "console-shell-signin__label";
  credLabel.textContent = "Credential";
  const credValue = document.createElement("span");
  credValue.className = "console-shell-signin__value";
  credValue.textContent = tokenEvidence(getLiveToken());
  connWrap.append(connLabel, connHost, credLabel, credValue);

  // --- which workspace ---------------------------------------------------------------------------
  const wsLabel = document.createElement("span");
  wsLabel.className = "console-shell-signin__label";
  wsLabel.textContent = "Workspace";
  const lede = document.createElement("p");
  lede.className = "console-shell-signin__lede";
  lede.textContent =
    "This daemon is serving " +
    roots.length +
    " workspaces. Pick the one this window follows - runs and activity are filtered to it. Pool, cache and latency stay daemon-wide.";
  const list = document.createElement("div");
  list.className = "console-shell-signin__list";

  const choose = (root: string): void => {
    rememberPick(root);
    setWorkspaceScope(root);
    markAsked();
    close();
  };

  const suggested = lastPick();
  for (const root of roots) {
    const b = document.createElement("button");
    b.type = "button";
    b.className = "pf-v6-c-button pf-m-secondary console-shell-signin__choice";
    b.title = root;
    const name = document.createElement("span");
    name.className = "pf-v6-c-button__text";
    name.textContent = shortName(root);
    const path = document.createElement("span");
    path.className = "console-shell-signin__path";
    path.textContent = root;
    b.append(name, path);
    if (root === suggested) b.dataset.suggested = "";
    b.addEventListener("click", () => choose(root));
    list.append(b);
  }

  // Offered plainly, not buried: watching every workspace is a legitimate way to work, and hiding it
  // would push people into picking one they did not want just to get past the question.
  const all = document.createElement("button");
  all.type = "button";
  all.className = "pf-v6-c-button pf-m-link console-shell-signin__all";
  const allText = document.createElement("span");
  allText.className = "pf-v6-c-button__text";
  allText.textContent = "Watch all workspaces";
  all.append(allText);
  all.addEventListener("click", () => choose(ALL_WORKSPACES));

  body.append(connWrap, wsLabel, lede, list, all);
  box.append(header, body);
  bullseye.append(box);
  backdrop.append(bullseye);
  document.body.append(backdrop);

  const first =
    list.querySelector<HTMLElement>("[data-suggested]") ?? list.querySelector<HTMLElement>("button");
  first?.focus();

  function close(): void {
    backdrop.remove();
    asking = false;
    document.removeEventListener("keydown", onKey);
  }
  // Escape lands on the daemon-wide view rather than leaving the question open: dismissing has to go
  // somewhere, and the honest destination is the scope that hides nothing.
  function onKey(e: KeyboardEvent): void {
    if (e.key === "Escape") choose(ALL_WORKSPACES);
  }
  document.addEventListener("keydown", onKey);
}
