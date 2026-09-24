// token-exchange.ts - how the console comes to hold a console token and not a stronger one.
//
// Two ways in. A CLI-opened link carries a one-time code (#code=mgx_...), never a token: the
// code lives a minute and is spent on first use, so the opener's argv, a history file or a
// screen share holds nothing worth taking. redeemLinkCode trades it at the daemon for the
// console token it stands for. And a page that was handed the operator token itself (a pasted
// token, an old link) trades it once for a console token via TokenService, because the operator
// token also reaches /mcp and token management, which a browser has no business holding.
//
// WHY IT IS A SEPARATE MODULE from lib/daemon. daemon.ts documents that a page importing
// only its primitives is tree-shaken clear of the ConnectRPC transport code; importing
// TokenService there would put the token client in every surface bundle. Only the shell,
// which does the trading for every tab, pays for this one.
//
// A token's class is its prefix (mgo_ operator, mgs_ stored, mgl_ share link), and the prefix
// alone decides: only an mgo_ token is traded, and every failure to trade one is a failure the
// caller surfaces, a refusal included, because each leaves the operator credential in the
// browser.

import { timestampFromMs } from "@bufbuild/protobuf/wkt";
import { createClient } from "@connectrpc/connect";
import { Level, TokenService } from "@wire/token/v1alpha1/token_pb";
import { createDaemonTransport, getLiveToken, setLiveToken } from "./daemon";

// Outcome names what happened, so a caller can log or test it without inspecting storage.
export type Outcome = "no-token" | "already-scoped" | "exchanged" | "failed";

// mint returns a freshly minted console token's secret. Injectable so the decision logic
// below is testable without a transport or a daemon.
export type Mint = () => Promise<string>;

// OPERATOR_PREFIX marks the operator class (internal/auth/format.go).
const OPERATOR_PREFIX = "mgo_";

// ensureConsoleToken trades an operator token for a console token; any other token is left
// alone. The new secret is written to storage before anything else happens, and the operator
// token is only ever replaced by an overwrite of the same key, so there is no window in which
// the page holds neither.
export async function ensureConsoleToken(mint: Mint): Promise<Outcome> {
  const held = getLiveToken();
  if (held === null) return "no-token";
  if (!held.startsWith(OPERATOR_PREFIX)) return "already-scoped";
  let secret: string;
  try {
    secret = await mint();
  } catch {
    // reported: by the daemon transport, and the caller reports "failed", a refusal included.
    return "failed";
  }
  if (!secret || !setLiveToken(secret)) return "failed";
  return "exchanged";
}

// CONSOLE_TOKEN_TTL_MS is the lifetime the console asks for: the default for a stored token
// (auth.DefaultTokenTTL). The daemon requires an expiry and refuses one past auth.MaxTokenTTL
// (366 days) rather than shortening it, so this must stay under that.
export const CONSOLE_TOKEN_TTL_MS = 90 * 24 * 60 * 60 * 1000;

// exchangeOperatorToken wires ensureConsoleToken to the real daemon. The minted token carries no
// name (the daemon derives a unique one). When it expires the daemon answers 401, which signs the
// console out with a notice (lib/daemon signalAuthLost) rather than failing surface by surface.
export async function exchangeOperatorToken(host: string, now = Date.now()): Promise<Outcome> {
  const client = createClient(TokenService, createDaemonTransport(host, getLiveToken()));
  return ensureConsoleToken(async () => {
    const resp = await client.createToken({
      grant: { console: Level.WRITE },
      expireTime: timestampFromMs(now + CONSOLE_TOKEN_TTL_MS),
    });
    return resp.secret;
  });
}

// redeemLinkCode trades a link's one-time code for the console token it stands for, at the
// daemon that served the page, and stores it. The code is the credential, so it is sent in the
// body and nowhere else. Anything but a stored token is "failed": a used or expired code, a
// daemon that cannot be reached, a store that refused the write.
export async function redeemLinkCode(host: string, code: string): Promise<Outcome> {
  let res: Response;
  try {
    res = await fetch("http://" + host + "/api/v1/token/exchange", {
      method: "POST",
      cache: "no-store",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ code }),
    });
  } catch {
    // reported: the caller reports "failed" with what to do next
    return "failed";
  }
  if (!res.ok) return "failed";
  let token: unknown;
  try {
    token = ((await res.json()) as { token?: unknown }).token;
  } catch {
    // reported: the caller reports "failed"
    return "failed";
  }
  if (typeof token !== "string" || !token || !setLiveToken(token)) return "failed";
  return "exchanged";
}
