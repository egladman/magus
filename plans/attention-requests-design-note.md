# Design note: agent attention requests, human-first

Captured 2026-08-15 from Eli, mid-session, while the delegation and diff
workstreams were executing. Not yet implementation work; this file exists so
the idea survives with its constraints intact.

## The idea

Agents (via MCP) can ask for a human's attention on a specific UI component
when work genuinely needs a human in the loop - a decision, an answer, an
approval. The console reuses the tiling-window-manager focus ring to PULSE the
component. The request lands in a passive, persistent place (tray or footer
entry) the human visits when they choose.

## The constraint that defines it

No focus stealing, ever. No modal that suspends the workflow until answered
(the macOS pattern, named as the anti-goal). Multi-tasking is promoted, not
interrupted. Attention is requested, never taken.

## Why this is not a bolt-on

It is the third instance of a design law already running through this
codebase:

1. The MCP diff tool cannot move the cursor and cannot mark a hunk viewed:
   "read is a claim only the reader can make." Agent proposes, human disposes.
2. The deepseek-harness lesson shipped today: visibility is not authority;
   emit facts, never gates. A wrong gate blocks correct work; a wrong signal
   merely gets ignored.
3. This: an attention request grants nothing and blocks nothing. It is a fact
   ("a decision is wanted here") rendered where the human already lives.

The delegation skill's new no-return vocabulary applies verbatim: a request
the human never answers must TERMINATE in the agent's view (expired/declined
by silence after a TTL), not hang the agent. Cooperative answer and guaranteed
settlement are different channels; keep both.

## Sketch (to be validated by a scout before any code)

- MCP: one small request surface (shape TBD; respect "fold, don't add" - it
  may belong on an existing tool as an op rather than a new tool). Payload:
  target surface/component, reason (one line, human-readable), and what kind
  of answer is wanted. Returns a request id immediately; never blocks.
- Bridge/daemon: holds the pending-request set; console subscribes the same
  way it consumes session state today.
- Console: focus-ring pulse on the target tile (no focus change, no scroll
  hijack), an entry in a passive tray/footer with the reason, aria-live
  polite (the diff surface already established polite-not-assertive as the
  house rule). Answering happens through the component itself, on the
  human's schedule.
- TUI/terminal: the passive analogs only - tty already has notify plumbing;
  at most a bell/OSC 9 notification plus a line in the overview. Never
  repaint over what the human is reading.
- Agent side: poll/park on request state like any other session read; the
  state machine is pending -> answered | expired | withdrawn.

## Open questions for Eli

- Does an attention request belong to a session (dies with it) or to the
  daemon (survives reconnects)? Likely answer as of the same evening:
  workspace-scoped and disk-backed, per
  [stateless-mcp-design-note.md](stateless-mcp-design-note.md).
- Urgency tiers: is there any request class that justifies MORE than a pulse
  (sound? sticky tray position?), or is flat-by-design the point?
- TTL default for expiry, and whether expiry notifies the agent or is only
  visible on next read.
- Naming (verb-first doctrine applies; nothing here names itself yet).

## Status

Recorded only. The console focus-ring styling and tiling surfaces exist
(console/src/console/{main,tabs,layoutPrefs}.ts, styles/console.css). The
branch feat/attention-footer-parity-e62c37 is NOT prior art despite the name
(checked: its commits are Buzz conformance and docs-render work). Next step
when prioritized: a read-only scout over the console WM + bridge channel
mechanics, then an implementation plan in this file's successor.
