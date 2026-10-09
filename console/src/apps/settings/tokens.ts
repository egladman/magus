// tokens.ts - the Settings "Access tokens" section: a LIST + REVOKE view over the server's
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
import {
  CredentialClass,
  Level,
  TokenService,
  type Grant,
  type TokenInfo,
} from "@wire/token/v1alpha1/token_pb";
import {
  createServerTransport,
  getLiveToken,
  isCapabilityDenied,
  isUnreachable,
} from "../../lib/server";
import { showToast } from "../../lib/refresh-toast";
import { h } from "../../desktop/view";
import {
  renderConnectPrompt,
  renderEmptyMessage,
  type EmptyStateSlots,
} from "../../desktop/connectPrompt";
import { statusGlyph } from "../../ui/status";
import { confirmDanger } from "./confirm";

// classLabel names a token's class for the operator. The operator token never reaches a
// ListTokens response; the grant beside it says what each may do.
function classLabel(c: CredentialClass): string {
  switch (c) {
    case CredentialClass.STORED:
      return "Token";
    case CredentialClass.SHARE:
      return "Read-only share";
    case CredentialClass.EXCHANGE:
      return "Link code";
    default:
      return "Unknown";
  }
}

// grantLabel renders a grant the way the CLI does ("mcp=write,console=read"), naming only the
// scopes it reaches.
function grantLabel(g: Grant | undefined): string {
  if (!g) return "nothing";
  const level = (l: Level): string =>
    l === Level.WRITE ? "write" : l === Level.READ ? "read" : "";
  const parts: string[] = [];
  for (const [scope, l] of [
    ["tokens", g.tokens],
    ["mcp", g.mcp],
    ["console", g.console],
  ] as const) {
    if (level(l)) parts.push(scope + "=" + level(l));
  }
  return parts.join(",") || "nothing";
}

// expiryLabel renders a token's expiry as a local date-time. Every token the server lists
// expires; "Never expires" is only what an unset timestamp would mean.
function expiryLabel(t: TokenInfo): string {
  const ts = t.expireTime;
  if (!ts) return "Never expires";
  const ms = Number(ts.seconds) * 1000 + Math.floor((ts.nanos || 0) / 1e6);
  return new Date(ms).toLocaleString();
}

const PURPOSE = "Connect the console to a running server to list and revoke its access tokens.";

// buildTokensSection builds the section body and drives it live against the server at host.
// A null host (no server resolved) short-circuits to the same connect prompt every other app shows.
// Returns the body element and a destroy() the app calls on teardown so a late RPC never
// renders into a detached node. opts.onDenied fires when the server declines the token service to
// this client (a phone-share session): the caller hides the whole section, so the SERVER, not a
// client-side mode guess, decides whether token management is offered.
export function buildTokensSection(
  host: string | null,
  opts: { onDenied?: () => void } = {},
): { el: HTMLElement; destroy(): void } {
  const body = h("div", "console-settings-tokens");
  let stale = false;

  if (!host) {
    const empty = buildEmpty();
    renderConnectPrompt(empty.slots, { connection: "none" }, { purpose: PURPOSE });
    body.append(empty.root);
    return {
      el: body,
      destroy() {
        stale = true;
      },
    };
  }

  const client: Client<typeof TokenService> = createClient(
    TokenService,
    createServerTransport(host, getLiveToken()),
  );

  // renderList repaints the whole body from a fresh ListTokens. It is called on mount and after
  // every successful revoke, so the list always reflects the server's current tokens.
  async function renderList(): Promise<void> {
    try {
      const resp = await client.listTokens({});
      if (stale) return;
      const tokens = resp.tokens;
      body.replaceChildren();
      if (tokens.length === 0) {
        const empty = buildEmpty();
        renderEmptyMessage(
          empty.slots,
          "No connector or share tokens",
          "The server has no connector tokens and no active share. Mint a connector token from the CLI with: magus config mcp connector. The built-in operator token is managed by the CLI and is never shown here.",
        );
        body.append(empty.root);
        return;
      }
      body.append(buildTable(tokens));
    } catch (e) {
      if (stale) return;
      // The server declined the service to this client (a read-only phone share): hide the section
      // entirely rather than show a failure - the server has decided token management is not offered.
      if (isCapabilityDenied(e)) {
        opts.onDenied?.();
        return;
      }
      // reported: by the server transport's failure interceptor; this is the text where the reader is looking
      const msg = e instanceof Error ? e.message : String(e);
      const empty = buildEmpty();
      if (isUnreachable(e)) {
        renderConnectPrompt(
          empty.slots,
          { connection: "disconnected", host: host ?? "", reason: msg },
          { onRetry: () => void renderList() },
        );
      } else {
        renderEmptyMessage(
          empty.slots,
          "Could not load tokens",
          "The server at " +
            host +
            " did not answer the token service (" +
            msg +
            "). It must be running with connector auth.",
        );
        const retry = h("button", "pf-v6-c-button pf-m-primary", "Retry");
        retry.type = "button";
        retry.addEventListener("click", () => void renderList());
        empty.slots.actions.append(retry);
      }
      body.replaceChildren(empty.root);
    }
  }

  // buildTable renders one row per token as a PF compact table: type, grant, name, id, expiry, and a
  // Revoke row action. The wrapper is the container PF's grid layout keys on, so a narrow pane stacks
  // the rows with a data label on each value rather than overflowing.
  function buildTable(tokens: TokenInfo[]): HTMLElement {
    const wrap = h("div", "console-settings-tokens__table");
    const table = h("table", "pf-v6-c-table pf-m-compact pf-m-grid-md");
    table.setAttribute("role", "grid");
    table.setAttribute("aria-label", "Access tokens");

    const thead = h("thead", "pf-v6-c-table__thead");
    const head = h("tr", "pf-v6-c-table__tr");
    head.setAttribute("role", "row");
    for (const label of ["Class", "Grant", "Name", "ID", "Expires", "Actions"]) {
      const th = h("th", "pf-v6-c-table__th");
      th.setAttribute("role", "columnheader");
      th.scope = "col";
      if (label === "Actions") th.append(h("span", "pf-v6-screen-reader", label));
      else th.textContent = label;
      head.append(th);
    }
    thead.append(head);

    const tbody = h("tbody", "pf-v6-c-table__tbody");
    tbody.setAttribute("role", "rowgroup");
    for (const t of tokens) {
      const row = h("tr", "pf-v6-c-table__tr");
      row.setAttribute("role", "row");
      const cell = (label: string, extra = ""): HTMLElement => {
        const td = h("td", "pf-v6-c-table__td" + extra);
        td.setAttribute("role", "cell");
        td.dataset.label = label;
        return td;
      };

      const type = cell("Class");
      const label = h("span", "pf-v6-c-label pf-m-outline pf-m-compact");
      label.append(h("span", "pf-v6-c-label__content", classLabel(t.class)));
      type.append(label);

      const grant = cell("Grant");
      grant.textContent = grantLabel(t.grant);
      const name = cell("Name");
      name.textContent = t.name || "(unnamed)";
      const fp = cell("ID", " console-settings-tokens__fp");
      fp.textContent = t.id;
      const exp = cell("Expires");
      exp.textContent = expiryLabel(t);

      const actionCell = cell("Actions", " pf-m-action");
      const revoke = h("button", "pf-v6-c-button pf-m-secondary pf-m-danger", "Revoke");
      revoke.type = "button";
      // The label reads "Revoke"; the accessible name says WHICH token.
      const who = (t.name ? t.name : classLabel(t.class)) + " (" + t.id + ")";
      revoke.setAttribute("aria-label", "Revoke token " + who);
      revoke.addEventListener("click", () => void revokeToken(t, revoke));
      actionCell.append(revoke);

      row.append(type, grant, name, fp, exp, actionCell);
      tbody.append(row);
    }
    table.append(thead, tbody);
    wrap.append(table);
    return wrap;
  }

  // revokeToken confirms, calls RevokeToken by fingerprint, then reloads the list. Revoking the
  // share token also closes its LAN listener - the server handles that teardown, not the UI.
  async function revokeToken(t: TokenInfo, btn: HTMLButtonElement): Promise<void> {
    const who = t.name ? t.name : classLabel(t.class);
    const isShare = t.class === CredentialClass.SHARE;
    const ok = await confirmDanger({
      title: isShare ? "Revoke the share token?" : "Revoke this token?",
      message: isShare
        ? 'Revoking "' + who + '" also closes the read-only share listener immediately.'
        : 'Any client using "' +
          who +
          '" will stop working. This cannot be undone - mint a new one from the CLI if needed.',
      confirmLabel: "Revoke",
    });
    if (!ok || stale) return;
    btn.disabled = true;
    try {
      await client.revokeToken({ name: t.id });
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

  body.append(buildLoading());
  void renderList();
  return {
    el: body,
    destroy() {
      stale = true;
    },
  };
}

// buildLoading is the line shown until the first answer, so a slow server reads as work in
// progress rather than as an empty section.
function buildLoading(): HTMLElement {
  const line = h("p", "console-settings-tokens__loading");
  line.setAttribute("role", "status");
  line.append(statusGlyph("running"), " Loading tokens...");
  return line;
}

// buildEmpty builds the shared empty state shell: the PF structure the connect prompt and the
// console's other empty states fill. The title is an h3 because it sits under the section's h2.
function buildEmpty(): { root: HTMLElement; slots: EmptyStateSlots } {
  const root = h("div", "pf-v6-c-empty-state");
  const content = h("div", "pf-v6-c-empty-state__content");
  const header = h("div", "pf-v6-c-empty-state__header");
  const titleBox = h("div", "pf-v6-c-empty-state__title");
  const slots: EmptyStateSlots = {
    title: h("h3", "pf-v6-c-empty-state__title-text"),
    message: h("div", "pf-v6-c-empty-state__body"),
    actions: h("div", "pf-v6-c-empty-state__actions"),
  };
  slots.actions.dataset.emptyWays = "";
  titleBox.append(slots.title);
  header.append(titleBox);
  content.append(header, slots.message, slots.actions);
  root.append(content);
  return { root, slots };
}
