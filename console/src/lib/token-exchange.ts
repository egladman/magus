// token-exchange.ts - how the console comes to hold a console token.
//
// A CLI-opened link carries a one-time code (#code=mgx_...), never a token.
// The code lives seconds and is spent on first use, so the opener's argv holds
// nothing worth taking. lib/server refuses an operator token outright.

import { MagusError, fail, fromPromise, ok, type Result } from "../gen/magus/types";
import { setLiveToken } from "./server";

const failed = (message: string): Result<null> => fail(new MagusError({ code: "", message }));

// redeemLinkCode trades a link's one-time code for the console token it stands for, at the
// server that served the page, and stores it. The code is the credential, so it is sent in the
// body and nowhere else. Anything but a stored token is an error: a used or expired code, a
// server that cannot be reached, a store that refused the write.
export async function redeemLinkCode(host: string, code: string): Promise<Result<null>> {
  const sent = await fromPromise(
    fetch("http://" + host + "/api/v1/token/exchange", {
      method: "POST",
      cache: "no-store",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ code }),
    }),
  );
  if (sent.error) return sent;
  const res = sent.value;
  if (!res.ok) return failed("the server answered HTTP " + res.status);
  const body = await fromPromise(res.json() as Promise<{ token?: unknown }>);
  if (body.error) return body;
  const token = body.value.token;
  if (typeof token !== "string" || !token || !setLiveToken(token)) {
    return failed("the server sent no token the console could store");
  }
  return ok(null);
}
