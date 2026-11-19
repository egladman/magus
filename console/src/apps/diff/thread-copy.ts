// thread-copy.ts - copying one thread as `magus diff --thread` prints it, on the person's click.
//
// Nothing here sends it anywhere: fetching and copying are the whole act, and each failure is a
// toast.

import { fromPromise, ok, type Result } from "../../gen/magus/types";
import { reportFailure } from "../../lib/notifications";
import { fetchThreadText } from "./session";

// copyThreadText fetches the thread threadId names and puts its text on the clipboard. It
// resolves to the error that stopped it, or null once the text reached the clipboard. write is the
// clipboard, injectable because a test has none.
export async function copyThreadText(
  host: string,
  threadId: string,
  signal: AbortSignal,
  write: (text: string) => Promise<void> = (text) => navigator.clipboard.writeText(text),
): Promise<Result<null>> {
  const text = await fetchThreadText(host, threadId, signal);
  if (text.error) return text;
  const written = await fromPromise(Promise.resolve().then(() => write(text.value)));
  if (written.error) {
    reportFailure(
      "Review",
      `Could not copy the thread: ${written.error.message}`,
      "thread:clipboard",
    );
    return written;
  }
  return ok(null);
}
