// tokens.ts - the Settings "Access tokens" section: a LIST + REVOKE view over the daemon's
// stored tokens and the active share link, spoken to over magus.token.v1alpha1.TokenService.
//
// It has no mint control. The service needs tokens=write, which only the operator token holds,
// and a console link never carries the operator token, so this page is normally refused and the
// section hidden (opts.onDenied). The operator token is structurally absent from ListTokens and
// unrevokable, so it never appears and is never a revoke target.
//
// Everything shown - names, ids, grants - is rendered through textContent (via h()), never as
// HTML, so a token name can carry no markup into the page.

import { createClient, type Client } from "@connectrpc/connect";
import { TokenService, TokenScope, type TokenInfo } from "@wire/token/v1alpha1/token_pb";
import { createDaemonTransport, getLiveToken, isCapabilityDenied } from "../../lib/daemon";
import { showToast } from "../../lib/refresh-toast";
import { h } from "../view";

// scopeLabel names a token's preset grant for the operator. The operator token never reaches a
// ListTokens response; an unspecified scope falls back to a plain "Token", and the grant column
// says what it may do.
function scopeLabel(scope: TokenScope): string {
  switch (scope) {
    case TokenScope.CONNECTOR:
      return "Connector";
    case TokenScope.CONSOLE:
      return "Console";
    case TokenScope.CONSOLE_READ:
      return "Viewer";
    case TokenScope.SHARE_READ:
      return "Read-only share";
    default:
      return "Token";
  }
}

// expiryLabel renders a token's expiry as a local date-time. Every token the daemon lists
// expires; "Never expires" is only what an unset timestamp would mean.
function expiryLabel(t: TokenInfo): string {
  const ts = t.expireTime;
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
export function buildTokensSection(
  host: string | null,
  opts: { onDenied?: () => void } = {},
): { el: HTMLElement; destroy(): void } {
  const body = h("div", "console-settings-tokens");
  let stale = false;

  if (!host) {
    body.append(
      buildEmpty(
        "Not connected to a daemon",
        "Connect the console to a running daemon to list and revoke its access tokens. Open the console from a magus link, or set the daemon host on the General tab.",
      ),
    );
    return {
      el: body,
      destroy() {
        stale = true;
      },
    };
  }

  const client: Client<typeof TokenService> = createClient(
    TokenService,
    createDaemonTransport(host, getLiveToken()),
  );

  // renderList repaints the whole body from a fresh ListTokens. It is called on mount and after
  // every successful revoke, so the list always reflects the daemon's current tokens.
  async function renderList(): Promise<void> {
    try {
      const resp = await client.listTokens({});
      if (stale) return;
      const tokens = resp.tokens;
      body.replaceChildren();
      if (tokens.length === 0) {
        body.append(
          buildEmpty(
            "No connector or share tokens",
            "The daemon has no connector tokens and no active share. Mint a connector token from the CLI with: magus config mcp connector. The built-in operator token is managed by the CLI and is never shown here.",
          ),
        );
        return;
      }
      body.append(buildTable(tokens));
    } catch (e) {
      if (stale) return;
      // The daemon declined the service to this client (a read-only phone share): hide the section
      // entirely rather than show a failure - the server has decided token management is not offered.
      if (isCapabilityDenied(e)) {
        opts.onDenied?.();
        return;
      }
      const msg = e instanceof Error ? e.message : String(e);
      body.replaceChildren(
        buildEmpty(
          "Could not load tokens",
          "The daemon at " +
            host +
            " did not answer the token service (" +
            msg +
            "). It must be running with connector auth. Start it with: magus server start.",
        ),
      );
    }
  }

  // buildTable renders one row per token: type, name, fingerprint, expiry, and a Revoke button.
  function buildTable(tokens: TokenInfo[]): HTMLElement {
    const table = h("div", "console-settings-tokens__table");
    table.setAttribute("role", "table");
    table.setAttribute("aria-label", "Access tokens");

    const head = h("div", "console-settings-tokens__row console-settings-tokens__row--head");
    head.setAttribute("role", "row");
    for (const label of ["Type", "Name", "ID", "Expires", ""]) {
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
      const label = h("span", "pf-v6-c-label pf-m-compact");
      label.append(h("span", "pf-v6-c-label__content", scopeLabel(t.scope)));
      if (t.grant) label.title = t.grant;
      type.append(label);

      const name = h(
        "span",
        "console-settings-tokens__cell console-settings-tokens__name",
        t.name || "(unnamed)",
      );
      name.setAttribute("role", "cell");

      const fp = h(
        "span",
        "console-settings-tokens__cell console-settings-tokens__fp",
        t.identifier,
      );
      fp.setAttribute("role", "cell");

      const exp = h(
        "span",
        "console-settings-tokens__cell console-settings-tokens__expiry",
        expiryLabel(t),
      );
      exp.setAttribute("role", "cell");

      const actionCell = h("span", "console-settings-tokens__cell");
      actionCell.setAttribute("role", "cell");
      const revoke = h(
        "button",
        "pf-v6-c-button pf-m-secondary pf-m-small",
        "Revoke",
      ) as HTMLButtonElement;
      revoke.type = "button";
      // The label already reads "Revoke"; the descriptive title/aria-label names WHICH token so the
      // control's effect is unambiguous (the repo's explicit-labeling standard).
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
      ? 'Revoke the share token "' +
        who +
        '"? This also closes the read-only share listener immediately.'
      : 'Revoke connector token "' +
        who +
        '"? Any client using it will stop working. This cannot be undone - mint a new one from the CLI if needed.';
    if (!confirm(detail)) return;
    btn.disabled = true;
    try {
      await client.revokeToken({ name: t.identifier });
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
  return {
    el: body,
    destroy() {
      stale = true;
    },
  };
}

// buildEmpty renders the shared console empty state: a PF EmptyState with a title and a body
// line. Reused for the not-connected, no-tokens, and load-failure cases.
function buildEmpty(title: string, sub: string): HTMLElement {
  const wrap = h("div", "pf-v6-c-empty-state");
  const content = h("div", "pf-v6-c-empty-state__content");
  content.append(
    h("h2", "pf-v6-c-empty-state__title-text", title),
    (() => {
      const b = h("div", "pf-v6-c-empty-state__body");
      b.append(h("p", "", sub));
      return b;
    })(),
  );
  wrap.append(content);
  return wrap;
}
