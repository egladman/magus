// transcript.ts - reads back the body magus wrote for a captured conversation.
//
// A capture's structure (who said what, about which file) exists once, in the note's prose,
// because a note file has to stay readable markdown in an editor or a vault - and a second
// structured copy in frontmatter is a copy that drifts from the prose the moment anyone
// touches either one.
//
// So the surface recovers the structure by parsing. That is safe HERE and nowhere else in
// this store: the format is magus's own output, and Note.source.kind says which format it is,
// so this is a program reading back what it wrote rather than guessing at a person's prose. A
// note without a source block is never fed to this.
//
// Every failure returns null and the caller falls back to printing the body verbatim, which
// is what the surface did before this existed. A capture the parser does not recognize must
// still be READABLE; losing the boxes is a cosmetic regression, losing the transcript is not.

// SUPPORTED_KINDS gates the parse. A kind minted by a newer magus reaches the fallback rather
// than being parsed by rules that predate it - which is the whole reason source.kind is a
// string on the wire instead of an enum.
const SUPPORTED_KINDS = new Set(["review-thread"]);

export interface TranscriptEntry {
  // subject is the file the entry was about, and locator narrows it ("hunk 3"). Both come
  // from the setext heading that opens a run of entries.
  subject: string;
  locator: string;
  author: string;
  resolved: boolean;
  body: string;
}

export interface Transcript {
  // preamble is everything before the first heading: what this is, and where it came from.
  preamble: string;
  entries: TranscriptEntry[];
}

// SETEXT_RULE matches the underline magus writes beneath a subject heading. Three or more, so
// a line of dashes inside quoted prose is unlikely to be mistaken for one.
const SETEXT_RULE = /^-{3,}$/;
// ATTRIBUTION matches "name:" on a line of its own, optionally carrying "(resolved)".
const ATTRIBUTION = /^(.+?)(\s*\(resolved\))?:$/;

// parseTranscript recovers the entries from a captured note body, or null when the body is
// not one this knows how to read.
export function parseTranscript(kind: string, body: string): Transcript | null {
  if (!SUPPORTED_KINDS.has(kind) || !body) return null;

  const lines = body.split("\n");
  const entries: TranscriptEntry[] = [];
  const preamble: string[] = [];

  let subject = "";
  let locator = "";
  let current: TranscriptEntry | null = null;
  let seenHeading = false;

  const flush = (): void => {
    if (!current) return;
    current.body = current.body.trim();
    if (current.body) entries.push(current);
    current = null;
  };

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i] ?? "";
    const next = lines[i + 1] ?? "";

    // A subject heading is a non-empty line underlined by a rule. Checked before attribution
    // so a path that happens to end in a colon cannot be read as a speaker.
    if (line.trim() && SETEXT_RULE.test(next.trim())) {
      flush();
      seenHeading = true;
      const heading = line.trim();
      const hunk = heading.match(/^(.*?)\s+(hunk\s+\d+)$/);
      subject = hunk?.[1] ?? heading;
      locator = hunk?.[2] ?? "";
      i++; // consume the rule
      continue;
    }

    const attributed = line.trim() ? ATTRIBUTION.exec(line.trim()) : null;
    if (attributed && seenHeading) {
      flush();
      current = {
        subject,
        locator,
        author: (attributed[1] ?? "").trim(),
        resolved: Boolean(attributed[2]),
        body: "",
      };
      continue;
    }

    if (current) current.body += line + "\n";
    else if (!seenHeading) preamble.push(line);
  }
  flush();

  // No entries means the body did not have the shape this claims to read, whatever its kind
  // said. Falling back beats rendering a transcript as one empty box.
  if (entries.length === 0) return null;
  return { preamble: preamble.join("\n").trim(), entries };
}
