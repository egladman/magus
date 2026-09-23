// token-exchange.ts - trades an operator token for a console-scoped one, once per token.
//
// WHY THIS EXISTS. The console authenticated with the operator token: the bootstrap
// credential that also opens /mcp and token management. Holding it in a browser makes the
// credential tiers theoretical - the whole point of a console tier is that a credential
// handed to a page cannot reach the agent tool surface. This swaps the page's copy for a
// token that opens the console and is refused at /mcp, so a leaked browser credential
// reaches strictly less.
//
// WHY IT IS A SEPARATE MODULE from lib/daemon. daemon.ts documents that a page importing
// only its primitives is tree-shaken clear of the ConnectRPC transport code; importing
// TokenService there would put the token client in every surface bundle. Only a surface
// that actually exchanges pays for this one.
//
// DETECTION IS BY ATTEMPT, NOT BY INSPECTION. Nothing in a token's bytes says which tier
// it belongs to, and the console must not try to guess. TokenService is mounted behind the
// operator-only guard, so a CreateToken call that SUCCEEDS is itself the proof the page
// held the operator token; a refusal means it already holds a scoped one and there is
// nothing to do. So a REFUSAL is never shown; any other failure is (see the shell's
// caller), because it leaves the operator credential in the browser.

import { timestampFromMs } from "@bufbuild/protobuf/wkt";
import { createClient } from "@connectrpc/connect";
import { TokenScope, TokenService } from "@wire/token/v1alpha1/token_pb";
import {
  createDaemonTransport,
  getLiveToken,
  hasScopedToken,
  isCapabilityDenied,
  markScopedToken,
  setLiveToken,
} from "./daemon";

// Outcome names what happened, so a caller can log or test it without inspecting storage.
// "denied" is the ordinary steady state once the swap has happened on another surface.
export type Outcome = "no-token" | "already-scoped" | "exchanged" | "denied" | "failed";

// mint returns a freshly minted console token's secret. Injectable so the decision logic
// below is testable without a transport or a daemon.
export type Mint = () => Promise<string>;

// ensureScopedToken performs the swap at most once.
//
// The ORDER is the safety property: the new secret is written to storage BEFORE the
// exchange is marked done, and the operator token is only ever replaced by an overwrite
// of the same key. There is no window in which the page has discarded the credential it
// had without having stored the one that replaces it. If the write fails, the mark is not
// set, so a later load retries rather than stranding the page with a token it did not keep.
export async function ensureScopedToken(mint: Mint): Promise<Outcome> {
  if (getLiveToken() === null) return "no-token";
  if (hasScopedToken()) return "already-scoped";

  let secret: string;
  try {
    secret = await mint();
  } catch (e) {
    // A refusal is the expected answer for a page that already holds a console token: the
    // operator-only mount declines it. Nothing is wrong and nothing should be surfaced.
    if (isCapabilityDenied(e)) {
      markScopedToken();
      return "denied";
    }
    // reported: by the daemon transport, and the caller reports "failed". The mark stays unset so
    // the next load retries with the credential the page still holds.
    return "failed";
  }

  if (!secret || !setLiveToken(secret)) return "failed";
  markScopedToken();
  return "exchanged";
}

// CONSOLE_TOKEN_TTL_MS is the lifetime the console asks for: the daemon's own ceiling for a
// browser-minted token (maxConsoleTokenTTL in internal/handler/token/service.go), which refuses a
// mint with no expiry. Asking for the ceiling rather than less keeps the page signed in as long as
// the daemon allows; the daemon clamps anything further out, so a drift here cannot mint a longer one.
export const CONSOLE_TOKEN_TTL_MS = 90 * 24 * 60 * 60 * 1000;

// exchangeOperatorToken wires ensureScopedToken to the real daemon. The minted token carries no
// name (the daemon derives a unique one). When it expires the daemon answers 401, which signs the
// console out with a notice (lib/daemon signalAuthLost) rather than failing surface by surface.
export async function exchangeOperatorToken(host: string, now = Date.now()): Promise<Outcome> {
  const client = createClient(TokenService, createDaemonTransport(host, getLiveToken()));
  return ensureScopedToken(async () => {
    const resp = await client.createToken({
      scope: TokenScope.CONSOLE,
      expireTime: timestampFromMs(now + CONSOLE_TOKEN_TTL_MS),
    });
    return resp.secret;
  });
}
