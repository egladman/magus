// tokens.ts - the Settings "Access tokens" section: a LIST + REVOKE view over the daemon's
// connector tokens and the active share token, spoken to over magus.token.v1.TokenService.
//
// It is VIEW-AND-REVOKE ONLY, matching the service: there is deliberately NO mint control.
// Minting a durable credential stays a CLI-only operation (`magus config mcp connector`), so
// the browser has no path to forge one - that is what closes the XSS-to-durable-credential
// escalation, and re-adding a mint button here would reopen it. The operator (cli) token is
// structurally absent from ListTokens and unrevokable, so it never appears and is never a
// revoke target.
//
// Everything shown - names, fingerprints - is rendered through textContent (via h()), never
// as HTML, so a token name can carry no markup into the page.

import { createClient, type Client } from "@connectrpc/connect";
import { TokenService, TokenScope, type TokenInfo } from "../../gen/magus/token/v1/token_pb";
import { createDaemonTransport, getLiveToken, isCapabilityDenied } from "../../lib/daemon";
import { showToast } from "../../lib/refresh-toast";
import { h } from "../view";

// scopeLabel names a token's class for the operator. Only connector and share tokens ever
// reach a ListTokens response (the operator token is invisible by construction); an
// unspecified/unknown scope falls back to a plain "Token" so a future class still renders.
function scopeLabel(scope: TokenScope): string {
  switch (scope) {
    case TokenScope.CONNECTOR: return "Connector";
    case TokenScope.SHARE_READ: return "Share to phone";
    default: return "Token";
  }
}

// expiryLabel renders a token's expiry as a local date-time, or "Never expires" when the
// expires timestamp is unset (a non-expiring connector token).
function expiryLabel(t: TokenInfo): string {
  const ts = t.expires;
  if (!ts) return "Never expires";
  const ms = Number(ts.seconds) * 1000 + Math.floor((ts.nanos || 0) / 1e6);
  return new Date(ms).toLocaleString();
}

// buildTokensSection builds the section body and drives it live against the daemon at host.
// A null host (no daemon resolved) short-circuits to a clear "connect first" empty state.
// Returns the body element and a destroy() the surface calls on teardown so a late RPC never
// renders into a detached node. opts.onDenied fires when the daemon declines the token service to
// this client (a phone-share session): the caller hides the whole section, so the SERVER, not a
// client-side mode guess, decides whether token management is offered.
export function buildTokensSection(host: string | null, opts: { onDenied?: () => void } = {}): { el: HTMLElement; destroy(): void } {
  const body = h("div", "console-settings-tokens");
  let stale = false;

  if (!host) {
    body.append(buildEmpty(
      "Not connected to a daemon",
      "Connect the console to a running daemon to list and revoke its access tokens. Open the console from a magus link, or set the daemon host on the General tab.",
    ));
    return { el: body, destroy() { stale = true; } };
  }

  const client: Client<typeof TokenService> = createClient(TokenService, createDaemonTransport(host, getLiveToken()));

  // renderList repaints the whole body from a fresh ListTokens. It is called on mount and after
  // every successful revoke, so the list always reflects the daemon's current tokens.
  async function renderList(): Promise<void> {
    try {
      const resp = await client.listTokens({});
      if (stale) return;
      const tokens = resp.tokens;
      body.replaceChildren();
      if (tokens.length === 0) {
        body.append(buildEmpty(
          "No connector or share tokens",
          "The daemon has no connector tokens and no active share. Mint a connector token from the CLI with: magus config mcp connector. The built-in operator token is managed by the CLI and is never shown here.",
        ));
        return;
      }
      body.append(buildTable(tokens));
    } catch (e) {
      if (stale) return;
      // The daemon declined the service to this client (a read-only phone share): hide the section
      // entirely rather than show a failure - the server has decided token management is not offered.
      if (isCapabilityDenied(e)) { opts.onDenied?.(); return; }
      const msg = e instanceof Error ? e.message : String(e);
      body.replaceChildren(buildEmpty(
        "Could not load tokens",
        "The daemon at " + host + " did not answer the token service (" + msg + "). It must be running with connector auth. Start it with: magus server start.",
      ));
    }
  }

  // buildTable renders one row per token: type, name, fingerprint, expiry, and a Revoke button.
  function buildTable(tokens: TokenInfo[]): HTMLElement {
    const table = h("div", "console-settings-tokens__table");
    table.setAttribute("role", "table");
    table.setAttribute("aria-label", "Access tokens");

    const head = h("div", "console-settings-tokens__row console-settings-tokens__row--head");
    head.setAttribute("role", "row");
    for (const label of ["Type", "Name", "Fingerprint", "Expires", ""]) {
      const cell = h("span", "console-settings-tokens__cell", label);
      cell.setAttribute("role", "columnheader");
      head.append(cell);
    }
    table.append(head);

    for (const t of tokens) {
      const row = h("div", "console-settings-tokens__row");
      row.setAttribute("role", "row");

      const type = h("span", "console-settings-tokens__cell");
      type.setAttribute("role", "cell");
      const label = h("span", "pf-v6-c-label pf-m-compact console-settings-tokens__type");
      label.append(h("span", "pf-v6-c-label__content", scopeLabel(t.scope)));
      type.append(label);

      const name = h("span", "console-settings-tokens__cell console-settings-tokens__name", t.name || "(unnamed)");
      name.setAttribute("role", "cell");

      const fp = h("span", "console-settings-tokens__cell console-settings-tokens__fp", t.identifier);
      fp.setAttribute("role", "cell");

      const exp = h("span", "console-settings-tokens__cell console-settings-tokens__expiry", expiryLabel(t));
      exp.setAttribute("role", "cell");

      const actionCell = h("span", "console-settings-tokens__cell");
      actionCell.setAttribute("role", "cell");
      const revoke = h("button", "pf-v6-c-button pf-m-secondary pf-m-small", "Revoke") as HTMLButtonElement;
      revoke.type = "button";
      // The label already reads "Revoke"; the descriptive title/aria-label names WHICH token so the
      // control's effect is unambiguous (the repo's explicit-labelling standard).
      const who = (t.name ? t.name : scopeLabel(t.scope)) + " (" + t.identifier + ")";
      revoke.title = "Revoke token " + who;
      revoke.setAttribute("aria-label", "Revoke token " + who);
      revoke.addEventListener("click", () => void revokeToken(t, revoke));
      actionCell.append(revoke);

      row.append(type, name, fp, exp, actionCell);
      table.append(row);
    }
    return table;
  }

  // revokeToken confirms, calls RevokeToken by fingerprint, then reloads the list. Revoking the
  // share token also closes its LAN listener - the server handles that teardown, not the UI.
  async function revokeToken(t: TokenInfo, btn: HTMLButtonElement): Promise<void> {
    const who = t.name ? t.name : scopeLabel(t.scope);
    const isShare = t.scope === TokenScope.SHARE_READ;
    const detail = isShare
      ? "Revoke the share token \"" + who + "\"? This also closes the phone share listener immediately."
      : "Revoke connector token \"" + who + "\"? Any client using it will stop working. This cannot be undone - mint a new one from the CLI if needed.";
    if (!confirm(detail)) return;
    btn.disabled = true;
    try {
      await client.revokeToken({ identifier: t.identifier });
      if (stale) return;
      showToast("Access tokens", "Revoked " + who + ".");
      await renderList();
    } catch (e) {
      if (stale) return;
      btn.disabled = false;
      const msg = e instanceof Error ? e.message : String(e);
      showToast("Access tokens", "Could not revoke " + who + ": " + msg, "error");
    }
  }

  void renderList();
  return { el: body, destroy() { stale = true; } };
}

// buildEmpty renders the shared console empty state: a PF EmptyState with a title and a body
// line. Reused for the not-connected, no-tokens, and load-failure cases.
function buildEmpty(title: string, sub: string): HTMLElement {
  const wrap = h("div", "pf-v6-c-empty-state");
  const content = h("div", "pf-v6-c-empty-state__content");
  content.append(
    h("h2", "pf-v6-c-empty-state__title-text", title),
    (() => { const b = h("div", "pf-v6-c-empty-state__body"); b.append(h("p", "", sub)); return b; })(),
  );
  wrap.append(content);
  return wrap;
}
