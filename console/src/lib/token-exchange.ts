// token-exchange.ts - how the console comes to hold a console token.
//
// A CLI-opened link carries a one-time code (#code=mgx_...), never a token.
// The code lives seconds and is spent on first use, so the opener's argv holds
// nothing worth taking. lib/server refuses an operator token outright.

import { setLiveToken } from "./server";

// Outcome names what happened, so a caller can log or test it without inspecting storage.
export type Outcome = "exchanged" | "failed";

// redeemLinkCode trades a link's one-time code for the console token it stands for, at the
// server that served the page, and stores it. The code is the credential, so it is sent in the
// body and nowhere else. Anything but a stored token is "failed": a used or expired code, a
// server that cannot be reached, a store that refused the write.
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
