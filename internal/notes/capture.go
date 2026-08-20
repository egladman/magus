package notes

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Capture turns a conversation into a note without the store learning where conversations
// come from.
//
// A review thread lives in internal/diff and dies with the session that held it: the store
// keeps which hunks were read and nothing else, so the comments are gone when the process
// is. That is the whole motivation, and it is also why this type is not types.DiffSession.
// A capture is prose plus provenance; the caller that HAS a session is the one that knows
// how to describe it, and keeping that mapping outside this package leaves room for a second
// source (a forge's review thread, say) without the notes store gaining a second opinion
// about what a conversation is.
type Capture struct {
	// Title is what a reader scans for months later. A capture with a generated title is
	// findable only by the code it touched, which is why the caller is asked for one.
	Title  string
	Source Source
	Tags   []string
	// Entries are in the order they should be read, which is the caller's judgement rather
	// than this package's - a review thread reads by file, a chat log by time.
	Entries []CaptureEntry
}

// CaptureEntry is one message. Author is a display name and nothing branches on it: a
// transcript records who said something, and a store that tried to VERIFY that would be
// making the authorship claim the capture exists to avoid making.
type CaptureEntry struct {
	// Subject is what the message was about, in the source's own terms - a file path for a
	// review comment. It becomes a file anchor, so a capture is findable from the code.
	Subject string
	// Locator narrows Subject within itself, rendered beside it and never parsed. A hunk
	// index for a review comment; empty when the source has no such notion.
	Locator string
	Author  string
	Body    string
	// Resolved marks a thread the participants closed. Kept because "we discussed this and
	// settled it" and "we discussed this and stopped" are different things to find later.
	Resolved bool
}

// Note renders the capture as a note under name.
//
// The body is markdown assembled here rather than by the caller, so every capture in a store
// reads the same way and a later reader can tell one at a glance. Anchors are derived: one
// file anchor per distinct subject, which is what makes a captured thread turn up when
// someone asks what is known about a file.
//
// Fails when there is nothing to capture. An empty transcript would be a note asserting that
// a conversation happened and declining to say what was said, and it would fail Validate for
// want of an anchor anyway - better to say which of the two went wrong.
func (c Capture) Note(name string) (Note, error) {
	if strings.TrimSpace(c.Title) == "" {
		return Note{}, errors.New("notes: a capture needs a title")
	}
	if len(c.Entries) == 0 {
		return Note{}, errors.New("notes: nothing to capture; this conversation has no messages")
	}
	if c.Source.Kind == "" {
		return Note{}, errors.New("notes: a capture needs a source kind")
	}

	src := c.Source
	if src.Captured.IsZero() {
		return Note{}, errors.New("notes: a capture needs the time it was taken")
	}
	// Seconds, in UTC. The sub-second half of a wall clock says nothing about when a
	// conversation happened and only makes the frontmatter noisy; normalizing the zone stops
	// the same capture reading as two different times on two machines.
	src.Captured = src.Captured.UTC().Truncate(time.Second)

	anchors, err := captureAnchors(c.Entries)
	if err != nil {
		return Note{}, err
	}
	n := Note{
		Name:    name,
		Title:   c.Title,
		Tags:    c.Tags,
		Anchors: anchors,
		Source:  &src,
		Body:    captureBody(c, src),
	}
	if err := Validate(n); err != nil {
		return Note{}, err
	}
	return n, nil
}

// captureAnchors derives one file anchor per distinct subject, in first-seen order made
// stable by a sort. Entries with no subject contribute none: a message about nothing in
// particular is still worth keeping in the transcript, just not worth claiming an anchor for.
func captureAnchors(entries []CaptureEntry) ([]Anchor, error) {
	seen := map[string]bool{}
	subjects := []string{}
	for _, e := range entries {
		s := strings.TrimSpace(e.Subject)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		subjects = append(subjects, s)
	}
	if len(subjects) == 0 {
		return nil, errors.New("notes: this conversation names no file, so the capture would be unanchored and nobody would find it again")
	}
	sort.Strings(subjects)
	anchors := make([]Anchor, 0, len(subjects))
	for _, s := range subjects {
		anchors = append(anchors, Anchor{Kind: AnchorFile, Target: s})
	}
	return anchors, nil
}

// captureBody renders the transcript.
//
// The preamble is not decoration. A note is prose someone stands behind, and this file is the
// exception; a reader who meets it six months from now needs to know that before they quote
// it at somebody. The frontmatter says the same thing in a form tools read - this says it in
// the form a human reads.
//
// Headings are SETEXT (a rule of dashes under the text) and speakers are a plain "name:", so
// no line depends on a markdown renderer to make sense. A note body is untrusted by contract
// and every magus surface therefore prints it as text - a `## path` heading is a real heading
// only in an editor, and reads as literal hashes everywhere magus itself shows it. Setext is
// a heading to a renderer AND an underline to a reader, which is the only form that works in
// both places.
//
// Lines are not hard-wrapped for the same reason: the reader's pane picks the measure, and a
// pre-wrapped paragraph re-wraps into ragged half-lines in anything narrower.
func captureBody(c Capture, src Source) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Captured from a %s on %s.\n", src.Kind, src.Captured.Format(time.RFC3339))
	if src.Ref != "" || src.AsOf != "" {
		b.WriteString(captureProvenanceLine(src) + "\n")
	}
	b.WriteString("\nA transcript, not written prose: these are things people said, quoted, and nobody has revisited them since. Re-read the code before acting on any of it.\n")

	// Tracked with a flag rather than by comparing against a zero string, or an opening entry
	// with no subject would match the initial value and lose its heading.
	subject, started := "", false
	for _, e := range c.Entries {
		if s := strings.TrimSpace(e.Subject); !started || s != subject {
			subject, started = s, true
			head := captureHeading(e)
			b.WriteString("\n" + head + "\n" + strings.Repeat("-", len(head)) + "\n")
		}
		b.WriteString("\n" + captureAttribution(e) + ":\n\n")
		b.WriteString(strings.TrimSpace(e.Body) + "\n")
	}
	return b.String()
}

func captureProvenanceLine(src Source) string {
	parts := []string{}
	if src.Ref != "" {
		parts = append(parts, "Session "+src.Ref)
	}
	if src.AsOf != "" {
		parts = append(parts, "patch "+src.AsOf)
	}
	return strings.Join(parts, ", ") + "."
}

func captureHeading(e CaptureEntry) string {
	s := strings.TrimSpace(e.Subject)
	if s == "" {
		s = "Not about a particular file"
	}
	if loc := strings.TrimSpace(e.Locator); loc != "" {
		return s + " " + loc
	}
	return s
}

// captureAttribution names the speaker, and says when a thread was closed. An unnamed author
// is recorded as unattributed rather than guessed at: the point of a capture is that its
// provenance is checkable, and inventing a name would be the one thing that breaks that.
func captureAttribution(e CaptureEntry) string {
	who := strings.TrimSpace(e.Author)
	if who == "" {
		who = "unattributed"
	}
	if e.Resolved {
		return who + " (resolved)"
	}
	return who
}

// HunkLocator renders a hunk index the way a capture heading wants it. Here rather than at
// the call site so every source that has a notion of "the Nth chunk of a file" spells it the
// same way in the store.
func HunkLocator(hunk int) string {
	if hunk <= 0 {
		return ""
	}
	return "hunk " + strconv.Itoa(hunk)
}
