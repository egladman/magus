// thread-copy.ts - copying one thread as `magus diff --thread` prints it, on the person's click.
//
// Nothing here sends it anywhere: fetching and copying are the whole act, and each failure is a
// toast.

import { reportFailure } from "../../lib/notifications";
import { fetchThreadText } from "./session";

// copyThreadText fetches the thread threadId names and puts its text on the clipboard. It
// resolves true only when the text reached the clipboard. write is the clipboard, injectable
// because a test has none.
export async function copyThreadText(
  host: string,
  threadId: string,
  signal: AbortSignal,
  write: (text: string) => Promise<void> = (text) => navigator.clipboard.writeText(text),
): Promise<boolean> {
  const text = await fetchThreadText(host, threadId, signal);
  if (text === null) return false;
  try {
    await write(text);
    return true;
  } catch (e) {
    reportFailure(
      "Review",
      `Could not copy the thread: ${e instanceof Error ? e.message : String(e)}`,
      "thread:clipboard",
    );
    return false;
  }
}
