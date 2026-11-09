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

// AdvisoryKind is what one line of the advisories store records. See
// docs/decisions/0038-a-person-advises-every-session-through-the-hooks.md.
type AdvisoryKind string

const (
	// AdvisoryPost is a person's directive to every session.
	AdvisoryPost AdvisoryKind = "post"
	// AdvisoryRetract closes a post; nothing else does.
	AdvisoryRetract AdvisoryKind = "retract"
	// AdvisoryDelivered records that one host session was shown a post.
	AdvisoryDelivered AdvisoryKind = "delivered"
	// AdvisoryActed records that a session shown a post ran its Expect.
	AdvisoryActed AdvisoryKind = "acted"
)

var advisoryKinds = []AdvisoryKind{
	AdvisoryPost, AdvisoryRetract, AdvisoryDelivered, AdvisoryActed,
}

// AdvisoryEntry is one line of the advisories store.
//
// Text, By and Expect belong to a post. Session, Host and Event say where a
// delivery or an action happened. A command line is never stored: acted
// says only that the prefix matched.
type AdvisoryEntry struct {
	Kind    AdvisoryKind `json:"kind"`
	ID      string       `json:"id"`
	Ms      int64        `json:"ms"`
	Text    string       `json:"text,omitempty"`
	By      string       `json:"by,omitempty"`
	Expect  string       `json:"expect,omitempty"`
	Session string       `json:"session,omitempty"`
	Host    string       `json:"host,omitempty"`
	Event   string       `json:"event,omitempty"`
}

type advisoryEntryJSON AdvisoryEntry

// MarshalJSON refuses an entry no reader could fold. That is an unknown
// kind, no id, or a delivery or action naming no session.
func (e AdvisoryEntry) MarshalJSON() ([]byte, error) {
	if err := e.valid(); err != nil {
		return nil, err
	}
	return json.Marshal(advisoryEntryJSON(e))
}

// UnmarshalJSON applies MarshalJSON's checks to a line read back.
func (e *AdvisoryEntry) UnmarshalJSON(b []byte) error {
	var raw advisoryEntryJSON
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	if err := AdvisoryEntry(raw).valid(); err != nil {
		return err
	}
	*e = AdvisoryEntry(raw)
	return nil
}

func (e AdvisoryEntry) valid() error {
	if !slices.Contains(advisoryKinds, e.Kind) {
		return fmt.Errorf("advisory entry kind %q is not one of post, retract, delivered or acted", e.Kind)
	}
	if e.ID == "" {
		return fmt.Errorf("advisory %s entry has no id", e.Kind)
	}
	if (e.Kind == AdvisoryDelivered || e.Kind == AdvisoryActed) && e.Session == "" {
		return fmt.Errorf("advisory %s entry for %s names no session", e.Kind, e.ID)
	}
	return nil
}

// key keeps a post apart from its retraction, so the fold keeps the text.
func (e AdvisoryEntry) key() string {
	return string(e.Kind) + "\x00" + e.ID + "\x00" + e.Session
}

// Advisory is one post as a reader meets it. Delivered and Acted map each
// host session id to the time in ms it was shown the post or acted on it.
type Advisory struct {
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
func (a Advisory) Open() bool { return a.RetractedMs == 0 }

// AdvisoryStore is the advisories store of one repository.
type AdvisoryStore struct {
	lines file.JSONL[AdvisoryEntry]
}

const advisoryFile = "advisories.jsonl"

// AdvisoryDir resolves <XDG state>/magus/advisories/<repo>-<hash12>. It
// keys on repository identity like [Dir], so every worktree reads one store.
func AdvisoryDir(root string) (string, error) {
	base, err := xdg.StateDir()
	if err != nil {
		return "", fmt.Errorf("advisories: resolve state dir, set XDG_STATE_HOME to a writable absolute path: %w", err)
	}
	dir, err := vcs.StateDir(base, "advisories", root)
	if err != nil {
		return "", fmt.Errorf("advisories: %w", err)
	}
	return dir, nil
}

// NewAdvisoryStore is the store in dir. It touches nothing until used.
func NewAdvisoryStore(dir string) AdvisoryStore {
	return AdvisoryStore{lines: file.JSONL[AdvisoryEntry]{
		Path: filepath.Join(dir, advisoryFile),
		Key:  AdvisoryEntry.key,
	}}
}

// Load folds the store into its advisories, oldest post first. An absent
// store has none. A line that does not decode fails the read, so a caller
// can say the store is unreadable instead of answering that none is open.
func (s AdvisoryStore) Load() ([]Advisory, error) {
	entries, err := s.lines.Load()
	if err != nil {
		return nil, fmt.Errorf("advisories: read %s: %w", s.lines.Path, err)
	}
	return foldAdvisories(entries), nil
}

func foldAdvisories(entries []AdvisoryEntry) []Advisory {
	byID := map[string]*Advisory{}
	var order []string
	for _, e := range entries {
		if e.Kind != AdvisoryPost {
			continue
		}
		byID[e.ID] = &Advisory{ID: e.ID, Text: e.Text, By: e.By, Expect: e.Expect, PostedMs: e.Ms,
			Delivered: map[string]int64{}, Acted: map[string]int64{}}
		order = append(order, e.ID)
	}
	for _, e := range entries {
		a := byID[e.ID]
		if a == nil {
			continue
		}
		switch e.Kind {
		case AdvisoryRetract:
			a.RetractedMs = e.Ms
		case AdvisoryDelivered:
			a.Delivered[e.Session] = e.Ms
		case AdvisoryActed:
			a.Acted[e.Session] = e.Ms
		case AdvisoryPost:
		}
	}
	out := make([]Advisory, 0, len(order))
	for _, id := range order {
		out = append(out, *byID[id])
	}
	slices.SortStableFunc(out, func(a, b Advisory) int { return cmp.Compare(a.PostedMs, b.PostedMs) })
	return out
}

// AdvisoryID names a post by its text and the moment it was posted. The
// same words posted twice are two advisories, each shown again.
func AdvisoryID(text string, ms int64) string {
	sum := sha256.Sum256(fmt.Appendf(nil, "%s\x00%d", text, ms))
	return "adv-" + hex.EncodeToString(sum[:])[:12]
}

// Post records text as a person's directive to every session. The text is
// clamped to [MaxMessageBytes], and an empty one is refused.
func (s AdvisoryStore) Post(ctx context.Context, text, expect, by string, now time.Time) (Advisory, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return Advisory{}, errors.New("advisories: an advisory needs text")
	}
	ms := now.UnixMilli()
	e := AdvisoryEntry{Kind: AdvisoryPost, ID: AdvisoryID(text, ms), Ms: ms,
		Text: boundMessage(text), By: by, Expect: strings.TrimSpace(expect)}
	err := s.lines.Update(ctx, func([]AdvisoryEntry) []AdvisoryEntry { return []AdvisoryEntry{e} })
	if err != nil {
		return Advisory{}, fmt.Errorf("advisories: record post: %w", err)
	}
	return foldAdvisories([]AdvisoryEntry{e})[0], nil
}

// ErrNoAdvisory is a retraction whose id or prefix names no advisory.
var ErrNoAdvisory = errors.New("no such advisory")

// ErrRetracted is a retraction of a closed advisory: one closes once.
var ErrRetracted = errors.New("advisory already retracted")

// Retract closes the advisory ref names, by id or unambiguous id prefix. An
// ambiguous prefix is refused and lists every match.
func (s AdvisoryStore) Retract(ctx context.Context, ref, by string, now time.Time) (Advisory, error) {
	var got Advisory
	var refused error
	err := s.lines.Update(ctx, func(entries []AdvisoryEntry) []AdvisoryEntry {
		got, refused = pickAdvisory(foldAdvisories(entries), ref)
		if refused != nil {
			return nil
		}
		got.RetractedMs = now.UnixMilli()
		return []AdvisoryEntry{{Kind: AdvisoryRetract, ID: got.ID, Ms: got.RetractedMs, By: by}}
	})
	if err != nil {
		return Advisory{}, fmt.Errorf("advisories: record retraction: %w", err)
	}
	return got, refused
}

func pickAdvisory(all []Advisory, ref string) (Advisory, error) {
	var matches []Advisory
	for _, a := range all {
		if a.ID == ref {
			matches = []Advisory{a}
			break
		}
		if strings.HasPrefix(a.ID, ref) {
			matches = append(matches, a)
		}
	}
	switch {
	case ref == "" || len(matches) == 0:
		return Advisory{}, fmt.Errorf("%w matches %q", ErrNoAdvisory, ref)
	case len(matches) > 1:
		ids := make([]string, 0, len(matches))
		for _, a := range matches {
			ids = append(ids, a.ID)
		}
		return Advisory{}, fmt.Errorf("%q matches %d advisories (%s); name one", ref, len(ids), strings.Join(ids, ", "))
	case !matches[0].Open():
		at := time.UnixMilli(matches[0].RetractedMs).Format(time.RFC3339)
		return matches[0], fmt.Errorf("%w: %s was retracted at %s", ErrRetracted, matches[0].ID, at)
	}
	return matches[0], nil
}

// Showing is where a hook stands when the guard asks what to show.
type Showing struct {
	Session string
	Host    string
	Event   string
}

// Deliver returns the open advisories not yet shown to at's session, oldest
// first. It records each delivery under the store's lock, so two hooks
// firing at once show it once. An empty session id receives nothing.
func (s AdvisoryStore) Deliver(ctx context.Context, at Showing, now time.Time) ([]Advisory, error) {
	if at.Session == "" {
		return nil, nil
	}
	var shown []Advisory
	err := s.lines.Update(ctx, func(entries []AdvisoryEntry) []AdvisoryEntry {
		var add []AdvisoryEntry
		for _, a := range foldAdvisories(entries) {
			if _, seen := a.Delivered[at.Session]; seen || !a.Open() {
				continue
			}
			shown = append(shown, a)
			add = append(add, at.entry(AdvisoryDelivered, a.ID, now))
		}
		return add
	})
	if err != nil {
		return nil, fmt.Errorf("advisories: record delivery: %w", err)
	}
	return shown, nil
}

func (at Showing) entry(kind AdvisoryKind, id string, now time.Time) AdvisoryEntry {
	return AdvisoryEntry{Kind: kind, ID: id, Ms: now.UnixMilli(), Session: at.Session, Host: at.Host, Event: at.Event}
}

// RecordActed records an action on each open advisory shown to at's session
// whose Expect prefixes command and that the session has not acted on. It
// reads before it locks, so a session with nothing pending costs one read.
func (s AdvisoryStore) RecordActed(ctx context.Context, at Showing, command string, now time.Time) error {
	if at.Session == "" {
		return nil
	}
	command = strings.TrimSpace(command)
	all, err := s.Load()
	if err != nil || len(pendingActs(all, at.Session, command)) == 0 {
		return err
	}
	err = s.lines.Update(ctx, func(entries []AdvisoryEntry) []AdvisoryEntry {
		var add []AdvisoryEntry
		for _, a := range pendingActs(foldAdvisories(entries), at.Session, command) {
			add = append(add, at.entry(AdvisoryActed, a.ID, now))
		}
		return add
	})
	if err != nil {
		return fmt.Errorf("advisories: record action: %w", err)
	}
	return nil
}

func pendingActs(all []Advisory, session, command string) []Advisory {
	var out []Advisory
	for _, a := range all {
		_, shown := a.Delivered[session]
		_, acted := a.Acted[session]
		if a.Open() && shown && !acted && a.Expect != "" && strings.HasPrefix(command, a.Expect) {
			out = append(out, a)
		}
	}
	return out
}

// Directive is the text a session is shown for advisories. It says outright
// that these are the person's own words, because a session is right to
// discount an instruction another agent relays.
func Directive(advisories []Advisory) string {
	var b strings.Builder
	for i, a := range advisories {
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
