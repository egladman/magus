// thread-brief.ts - copying a thread's brief, on the person's click.
//
// The brief is the text the person carries to their own model. Nothing here sends it anywhere:
// fetching and copying are the whole act, and each failure is a toast.

import { reportFailure } from "../../lib/notifications";
import { fetchThreadBrief } from "./session";

// copyThreadBrief fetches the brief for the thread threadId names and puts it on the clipboard.
// It resolves true only when the text reached the clipboard. write is the clipboard, injectable
// because a test has none.
export async function copyThreadBrief(
  host: string,
  threadId: string,
  signal: AbortSignal,
  write: (text: string) => Promise<void> = (text) => navigator.clipboard.writeText(text),
): Promise<boolean> {
  const brief = await fetchThreadBrief(host, threadId, signal);
  if (brief === null) return false;
  try {
    await write(brief);
    return true;
  } catch (e) {
    reportFailure(
      "Review",
      `Could not copy the brief: ${e instanceof Error ? e.message : String(e)}`,
      "thread:clipboard",
    );
    return false;
  }
}
