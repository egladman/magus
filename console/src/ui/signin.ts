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
    localStorage.setItem(LAST_KEY, root);
  } catch {
    // A forgotten preselection costs one extra click next time.
  }
}

// A credential is shown as evidence, never in full: enough to confirm one arrived and to tell two
// apart, never enough to read over a shoulder or lift from a screenshot.
//
// The no-token line says what was OBSERVED, not what the daemon is. It used to read "this daemon is
// open on loopback", which is a claim about the daemon's security posture inferred from an absence -
// all this function knows is that no token is stored. The inference happens to hold on this screen
// (a daemon that wanted one would have 401'd and there would be no workspace list to choose from),
// but the sentence outlives the screen, and it was already false the first time the dialog was opened
// from a path that had not talked to a daemon at all.
function tokenEvidence(token: string | null): string {
  if (!token) return "None. The daemon answered this window without asking for one.";
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
  // "not configured" read as a calm, ordinary state. It is not one: this screen only opens once a
  // daemon has answered with two or more workspaces, so an address must have resolved to get here.
  // If it did not, the console has lost track of which daemon answered - worth saying so rather than
  // reporting it in the same voice as a host that is present.
  const daemonHost = resolveDaemonHost();
  connHost.textContent = daemonHost ?? "not resolved";
  if (!daemonHost) {
    connHost.dataset.anomaly = "";
    connHost.title =
      "This workspace list came from a daemon, so an address should have resolved here.";
  }
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
  // Menu rows, not a stack of outlined buttons. Two choices rendered as two bordered cards read as two
  // things to STUDY rather than two things to pick, and at four or five they became a wall. Same PF
  // menu vocabulary as the title bar's own workspace control, so the place you first choose a
  // workspace and the place you change it afterwards look like one system.
  //
  // Still a list rather than a dropdown: this is asked once per browser tab, and a dropdown would cost
  // a click to reveal options that fit on screen anyway.
  const menu = document.createElement("div");
  menu.className = "pf-v6-c-menu console-shell-signin__menu";
  const menuContent = document.createElement("div");
  menuContent.className = "pf-v6-c-menu__content";
  const list = document.createElement("ul");
  list.className = "pf-v6-c-menu__list console-shell-signin__list";
  list.setAttribute("role", "menu");
  menuContent.append(list);
  menu.append(menuContent);

  const commit = (root: string): void => {
    // Only an explicit workspace pick moves the preselection. Escape and "All workspaces" both arrive
    // here with ALL_WORKSPACES, and letting either clear it would cost the NEXT browser tab its
    // one-click answer as the price of dismissing this one.
    if (root !== ALL_WORKSPACES) rememberPick(root);
    setWorkspaceScope(root);
    markAsked();
    close();
  };

  // Selecting and confirming are SEPARATE. Clicking a row used to apply it and close the dialog, so
  // the scope changed on the same click that was still reading the options - and there was no moment
  // where the answer was visible before it took effect. Now a row marks the choice and the button
  // commits it, which is also what lets a remembered pick be genuinely preselected rather than merely
  // focused: the answer is filled in, and the confirming click still happens.
  let picked: string | null = null;
  const rows: HTMLButtonElement[] = [];

  const select = (root: string): void => {
    picked = root;
    for (const r of rows) {
      const on = r.dataset.root === root;
      r.setAttribute("aria-checked", on ? "true" : "false");
      r.classList.toggle("pf-m-selected", on);
    }
    confirm.disabled = false;
  };

  const row = (root: string, text: string, path: string): HTMLLIElement => {
    const li = document.createElement("li");
    li.className = "pf-v6-c-menu__list-item";
    li.setAttribute("role", "none");
    const b = document.createElement("button");
    b.type = "button";
    b.className = "pf-v6-c-menu__item console-shell-signin__choice";
    b.setAttribute("role", "menuitemradio");
    b.setAttribute("aria-checked", "false");
    b.dataset.root = root;
    if (path) b.title = path;
    const main = document.createElement("span");
    main.className = "pf-v6-c-menu__item-main";
    const name = document.createElement("span");
    name.className = "pf-v6-c-menu__item-text";
    name.textContent = text;
    main.append(name);
    if (path) {
      // The path is the ONLY way to tell two checkouts sharing a leaf name apart, so it stays on its
      // own line under the name rather than becoming a tooltip.
      const p = document.createElement("span");
      p.className = "console-shell-signin__path";
      p.textContent = path;
      main.append(p);
    }
    b.append(main);
    b.addEventListener("click", () => select(root));
    // Double-click is the shortcut people reach for in any list-plus-confirm, and refusing it makes
    // the dialog feel stuck rather than careful.
    b.addEventListener("dblclick", () => commit(root));
    li.append(b);
    rows.push(b);
    return li;
  };

  const suggested = lastPick();
  list.append(
    // Daemon-wide FIRST, matching the title bar's own menu, and offered as a row rather than a link
    // below: watching every workspace is a legitimate way to work, and burying it would push people
    // into picking one they did not want just to get past the question.
    row(ALL_WORKSPACES, "All workspaces", ""),
    ...roots.map((r) => row(r, shortName(r), r)),
  );

  const footer = document.createElement("footer");
  footer.className = "pf-v6-c-modal-box__footer console-shell-signin__footer";
  const confirm = document.createElement("button");
  confirm.type = "button";
  confirm.className = "pf-v6-c-button pf-m-primary console-shell-signin__confirm";
  const confirmText = document.createElement("span");
  confirmText.className = "pf-v6-c-button__text";
  confirmText.textContent = "Connect";
  confirm.append(confirmText);
  // Disabled until something is chosen. A first visit with nothing remembered has to make an actual
  // choice - a scope nobody picked is the failure this screen exists to prevent, and defaulting the
  // button to "All workspaces" would hand out exactly that to anyone who pressed it on reflex.
  confirm.disabled = true;
  confirm.addEventListener("click", () => {
    if (picked !== null) commit(picked);
  });
  footer.append(confirm);

  if (suggested && roots.includes(suggested)) {
    const marked = rows.find((r) => r.dataset.root === suggested);
    if (marked) marked.dataset.suggested = "";
    select(suggested);
  }

  body.append(connWrap, wsLabel, lede, menu);
  box.append(header, body, footer);
  bullseye.append(box);
  backdrop.append(bullseye);
  document.body.append(backdrop);

  const first = list.querySelector<HTMLElement>("[data-suggested]") ?? rows[0];
  first?.focus();

  function close(): void {
    backdrop.remove();
    asking = false;
    document.removeEventListener("keydown", onKey);
  }
  // Escape lands on the daemon-wide view rather than leaving the question open: dismissing has to go
  // somewhere, and the honest destination is the scope that hides nothing.
  function onKey(e: KeyboardEvent): void {
    if (e.key === "Escape") {
      // Dismissing bypasses the confirm button on purpose: it is not a choice, it is a refusal to
      // make one, and the honest destination for that is the scope that hides nothing.
      commit(ALL_WORKSPACES);
      return;
    }
    if (e.key !== "Tab") return;
    // aria-modal tells a screen reader that nothing outside this box exists. Without a trap Tab walks
    // straight out into the console behind it, and the two accounts of where the user is disagree.
    const stops = [...box.querySelectorAll<HTMLElement>("button")];
    if (stops.length === 0) return;
    const edge = e.shiftKey ? stops[0] : stops[stops.length - 1];
    const wrapTo = e.shiftKey ? stops[stops.length - 1] : stops[0];
    const active = document.activeElement;
    if (active === edge || !(active instanceof Node) || !box.contains(active)) {
      e.preventDefault();
      wrapTo.focus();
    }
  }
  document.addEventListener("keydown", onKey);
}
