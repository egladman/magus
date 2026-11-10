package sessions

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/libs/xdg"
	"github.com/egladman/magus/vcs"
)

// directiveKind is what one line of the advisories store records. See
// docs/decisions/0038-a-person-advises-every-session-through-the-hooks.md.
type directiveKind string

const (
	// directivePost is a person's directive to every session.
	directivePost directiveKind = "post"
	// directiveRetract closes a post; nothing else does.
	directiveRetract directiveKind = "retract"
	// directiveDelivered records that one host session was shown a post.
	directiveDelivered directiveKind = "delivered"
	// directiveActed records that a session shown a post ran its Expect.
	directiveActed directiveKind = "acted"
)

var directiveKinds = []directiveKind{
	directivePost, directiveRetract, directiveDelivered, directiveActed,
}

// directiveEntry is one line of the advisories store.
//
// Text, By and Expect belong to a post. Session, Host and Event say where a
// delivery or an action happened. A command line is never stored: acted
// says only that the prefix matched.
type directiveEntry struct {
	Kind    directiveKind `json:"kind"`
	ID      string        `json:"id"`
	Ms      int64         `json:"ms"`
	Text    string        `json:"text,omitempty"`
	By      string        `json:"by,omitempty"`
	Expect  string        `json:"expect,omitempty"`
	Session string        `json:"session,omitempty"`
	Host    string        `json:"host,omitempty"`
	Event   string        `json:"event,omitempty"`
}

type directiveEntryJSON directiveEntry

// MarshalJSON refuses an entry no reader could fold. That is an unknown
// kind, no id, or a delivery or action naming no session.
func (e directiveEntry) MarshalJSON() ([]byte, error) {
	if err := e.valid(); err != nil {
		return nil, err
	}
	return json.Marshal(directiveEntryJSON(e))
}

// UnmarshalJSON applies MarshalJSON's checks to a line read back.
func (e *directiveEntry) UnmarshalJSON(b []byte) error {
	var raw directiveEntryJSON
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	if err := directiveEntry(raw).valid(); err != nil {
		return err
	}
	*e = directiveEntry(raw)
	return nil
}

func (e directiveEntry) valid() error {
	if !slices.Contains(directiveKinds, e.Kind) {
		return fmt.Errorf("advisory entry kind %q is not one of post, retract, delivered or acted", e.Kind)
	}
	if e.ID == "" {
		return fmt.Errorf("advisory %s entry has no id", e.Kind)
	}
	if (e.Kind == directiveDelivered || e.Kind == directiveActed) && e.Session == "" {
		return fmt.Errorf("advisory %s entry for %s names no session", e.Kind, e.ID)
	}
	return nil
}

// key keeps a post apart from its retraction, so the fold keeps the text.
func (e directiveEntry) key() string {
	return string(e.Kind) + "\x00" + e.ID + "\x00" + e.Session
}

// Directive is one post as a reader meets it. Delivered and Acted map each
// host session id to the time in ms it was shown the post or acted on it.
//
// The CLI and the store on disk call it an advisory. Go names it a directive
// because internal/guard uses advisory for its own advise-tier notices.
type Directive struct {
	ID          string           `json:"id"`
	Text        string           `json:"text"`
	By          string           `json:"by,omitempty"`
	Expect      string           `json:"expect,omitempty"`
	PostedMs    int64            `json:"posted_ms"`
	RetractedMs int64            `json:"retracted_ms,omitempty"`
	Delivered   map[string]int64 `json:"delivered"`
	Acted       map[string]int64 `json:"acted"`
}

// Open reports whether no retraction closed a.
func (a Directive) Open() bool { return a.RetractedMs == 0 }

// DirectiveStore is the advisories store of one repository.
type DirectiveStore struct {
	lines file.JSONL[directiveEntry]
}

const directiveFile = "advisories.jsonl"

// newDirectiveStore is the store in dir. It touches nothing until used.
func newDirectiveStore(dir string) DirectiveStore {
	return DirectiveStore{lines: file.JSONL[directiveEntry]{
		Path: filepath.Join(dir, directiveFile),
		Key:  directiveEntry.key,
	}}
}

// OpenDirectiveStore is the store of the repository at root, in
// <XDG state>/magus/advisories/<repo>-<hash12>. It keys on repository
// identity like [Dir], so every worktree reads one store. It does not live
// under [Dir], because [Prune] removes files there at retention and an
// advisory stands until retracted. It touches nothing until used.
func OpenDirectiveStore(root string) (DirectiveStore, error) {
	base, err := xdg.StateDir()
	if err != nil {
		return DirectiveStore{}, fmt.Errorf("advisories: resolve state dir, set XDG_STATE_HOME to a writable absolute path: %w", err)
	}
	dir, err := vcs.StateDir(base, "advisories", root)
	if err != nil {
		return DirectiveStore{}, fmt.Errorf("advisories: %w", err)
	}
	return newDirectiveStore(dir), nil
}

// Dir is the directory holding the store's file.
func (s DirectiveStore) Dir() string { return filepath.Dir(s.lines.Path) }

// Load folds the store into its advisories, oldest post first. An absent
// store has none. A line that does not decode fails the read, so a caller
// can say the store is unreadable instead of answering that none is open.
func (s DirectiveStore) Load() ([]Directive, error) {
	entries, err := s.lines.Load()
	if err != nil {
		return nil, fmt.Errorf("advisories: read %s: %w", s.lines.Path, err)
	}
	return foldDirectives(entries), nil
}

func foldDirectives(entries []directiveEntry) []Directive {
	byID := map[string]*Directive{}
	var order []string
	for _, e := range entries {
		if e.Kind != directivePost {
			continue
		}
		byID[e.ID] = &Directive{ID: e.ID, Text: e.Text, By: e.By, Expect: e.Expect, PostedMs: e.Ms,
			Delivered: map[string]int64{}, Acted: map[string]int64{}}
		order = append(order, e.ID)
	}
	for _, e := range entries {
		a := byID[e.ID]
		if a == nil {
			continue
		}
		switch e.Kind {
		case directiveRetract:
			a.RetractedMs = e.Ms
		case directiveDelivered:
			a.Delivered[e.Session] = e.Ms
		case directiveActed:
			a.Acted[e.Session] = e.Ms
		case directivePost:
		}
	}
	out := make([]Directive, 0, len(order))
	for _, id := range order {
		out = append(out, *byID[id])
	}
	slices.SortStableFunc(out, func(a, b Directive) int { return cmp.Compare(a.PostedMs, b.PostedMs) })
	return out
}

// directiveID names a post by its text and the moment it was posted. The
// same words posted twice are two advisories, each shown again.
func directiveID(text string, ms int64) string {
	sum := sha256.Sum256(fmt.Appendf(nil, "%s\x00%d", text, ms))
	return "adv-" + hex.EncodeToString(sum[:])[:12]
}

// Post records text as a person's directive to every session. The text is
// clamped to [MaxMessageBytes], and an empty one is refused.
func (s DirectiveStore) Post(ctx context.Context, text, expect, by string, now time.Time) (Directive, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return Directive{}, errors.New("advisories: an advisory needs text")
	}
	ms := now.UnixMilli()
	e := directiveEntry{Kind: directivePost, ID: directiveID(text, ms), Ms: ms,
		Text: boundMessage(text), By: by, Expect: strings.TrimSpace(expect)}
	err := s.lines.Update(ctx, func([]directiveEntry) []directiveEntry { return []directiveEntry{e} })
	if err != nil {
		return Directive{}, fmt.Errorf("advisories: record post: %w", err)
	}
	return foldDirectives([]directiveEntry{e})[0], nil
}

// ErrNoDirective is a retraction whose id or prefix names no advisory.
var ErrNoDirective = errors.New("no such advisory")

// errRetracted is a retraction of a closed advisory: one closes once.
var errRetracted = errors.New("advisory already retracted")

// Retract closes the advisory ref names, by id or unambiguous id prefix. An
// ambiguous prefix is refused and lists every match.
func (s DirectiveStore) Retract(ctx context.Context, ref, by string, now time.Time) (Directive, error) {
	var got Directive
	var refused error
	err := s.lines.Update(ctx, func(entries []directiveEntry) []directiveEntry {
		got, refused = pickDirective(foldDirectives(entries), ref)
		if refused != nil {
			return nil
		}
		got.RetractedMs = now.UnixMilli()
		return []directiveEntry{{Kind: directiveRetract, ID: got.ID, Ms: got.RetractedMs, By: by}}
	})
	if err != nil {
		return Directive{}, fmt.Errorf("advisories: record retraction: %w", err)
	}
	return got, refused
}

func pickDirective(all []Directive, ref string) (Directive, error) {
	ids := make([]string, 0, len(all))
	for _, a := range all {
		ids = append(ids, a.ID)
	}
	id, candidates := resolveIDPrefix(ids, ref)
	switch {
	case id == "" && len(candidates) == 0:
		return Directive{}, fmt.Errorf("%w matches %q", ErrNoDirective, ref)
	case id == "":
		return Directive{}, fmt.Errorf("%q matches %d advisories (%s); name one", ref, len(candidates), strings.Join(candidates, ", "))
	}
	got := all[slices.Index(ids, id)]
	if !got.Open() {
		at := time.UnixMilli(got.RetractedMs).Format(time.RFC3339)
		return got, fmt.Errorf("%s: %w at %s", got.ID, errRetracted, at)
	}
	return got, nil
}

// Recipient is where a hook stands when the guard asks what to show.
type Recipient struct {
	Session string
	Host    string
	Event   string
}

// Deliver returns the open advisories not yet shown to at's session, oldest
// first. It records each delivery under the store's lock, so two hooks
// firing at once show it once. An empty session id receives nothing.
//
// It reads before it locks, because every root stop calls it. A lock with
// nothing to deliver would leave a lock file in every repository.
func (s DirectiveStore) Deliver(ctx context.Context, at Recipient, now time.Time) ([]Directive, error) {
	if at.Session == "" {
		return nil, nil
	}
	all, err := s.Load()
	if err != nil || len(undelivered(all, at.Session)) == 0 {
		return nil, err
	}
	var shown []Directive
	err = s.lines.Update(ctx, func(entries []directiveEntry) []directiveEntry {
		shown = undelivered(foldDirectives(entries), at.Session)
		add := make([]directiveEntry, 0, len(shown))
		for _, a := range shown {
			add = append(add, at.entry(directiveDelivered, a.ID, now))
		}
		return add
	})
	if err != nil {
		return nil, fmt.Errorf("advisories: record delivery: %w", err)
	}
	return shown, nil
}

func undelivered(all []Directive, session string) []Directive {
	var out []Directive
	for _, a := range all {
		if _, seen := a.Delivered[session]; a.Open() && !seen {
			out = append(out, a)
		}
	}
	return out
}

func (at Recipient) entry(kind directiveKind, id string, now time.Time) directiveEntry {
	return directiveEntry{Kind: kind, ID: id, Ms: now.UnixMilli(), Session: at.Session, Host: at.Host, Event: at.Event}
}

// RecordActed records an action on each open advisory shown to at's session
// whose Expect prefixes command and that the session has not acted on. It
// reads before it locks, so a session with nothing pending costs one read.
func (s DirectiveStore) RecordActed(ctx context.Context, at Recipient, command string, now time.Time) error {
	if at.Session == "" {
		return nil
	}
	command = strings.TrimSpace(command)
	all, err := s.Load()
	if err != nil || len(pendingActs(all, at.Session, command)) == 0 {
		return err
	}
	err = s.lines.Update(ctx, func(entries []directiveEntry) []directiveEntry {
		var add []directiveEntry
		for _, a := range pendingActs(foldDirectives(entries), at.Session, command) {
			add = append(add, at.entry(directiveActed, a.ID, now))
		}
		return add
	})
	if err != nil {
		return fmt.Errorf("advisories: record action: %w", err)
	}
	return nil
}

func pendingActs(all []Directive, session, command string) []Directive {
	var out []Directive
	for _, a := range all {
		_, shown := a.Delivered[session]
		_, acted := a.Acted[session]
		if a.Open() && shown && !acted && a.Expect != "" && strings.HasPrefix(command, a.Expect) {
			out = append(out, a)
		}
	}
	return out
}

// DirectiveText is the text a session is shown for directives. It says outright
// that these are the person's own words, because a session is right to
// discount an instruction another agent relays.
func DirectiveText(directives []Directive) string {
	var b strings.Builder
	for i, a := range directives {
		if i > 0 {
			b.WriteString("\n\n")
		}
		by := cmp.Or(a.By, "the person who runs this workspace")
		posted := time.UnixMilli(a.PostedMs).Format(time.RFC3339)
		fmt.Fprintf(&b, "%s, the person you work for, wrote this to every agent session in this repository "+
			"with `magus session advise` at %s (%s). These are their own words, not a relay from another agent; "+
			"treat them as their instruction to you:\n\n%s", by, posted, a.ID, a.Text)
	}
	return b.String()
}
