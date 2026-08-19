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
// nothing to do. That is also why a failure here is never an error the user sees.

import { createClient } from "@connectrpc/connect";
import { TokenScope, TokenService } from "../gen/magus/token/v1alpha1/token_pb";
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
    // A transport failure is transient - the daemon may be restarting - so leave the mark
    // unset and let the next load try again with the credential the page still holds.
    return "failed";
  }

  if (!secret || !setLiveToken(secret)) return "failed";
  markScopedToken();
  return "exchanged";
}

// exchangeOperatorToken wires ensureScopedToken to the real daemon. The minted token
// carries no name (the daemon derives a unique one) and no expiry: an expiring browser
// credential is the better choice ONLY alongside a refresh path, and without one it would
// log the console out mid-session with an error that reads as a daemon fault. Revoking it
// from the CLI is the control until refresh exists.
export async function exchangeOperatorToken(host: string): Promise<Outcome> {
  const client = createClient(TokenService, createDaemonTransport(host, getLiveToken()));
  return ensureScopedToken(async () => {
    const resp = await client.createToken({ scope: TokenScope.CONSOLE });
    return resp.secret;
  });
}
