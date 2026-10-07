//go:build !wasm

package std

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// trailDefaultSince is the window a trail read covers when the caller names none: long
// enough for a working day's session, short enough that the trails it stats are few.
const trailDefaultSince = 24 * time.Hour

// cacheDirer is the one capability of the loaded workspace the trail read needs beyond
// its root: where its cache, and so its trail, lives.
type cacheDirer interface {
	CacheDir() string
}

// MagusTrailRead backs magus\trail.read: one session's guard record from every checkout's
// trail.
func MagusTrailRead(ctx context.Context, opts map[string]any) (types.FeedbackTrail, error) {
	ws := types.WorkspaceFromContext(ctx)
	if ws == nil {
		return types.FeedbackTrail{}, errNoWorkspace("trail.read")
	}
	w, err := feedbackWindow(opts, time.Now())
	if err != nil {
		return types.FeedbackTrail{}, err
	}
	root := ws.Root()
	cacheDir := filepath.Join(root, ".magus")
	if c, ok := ws.(cacheDirer); ok {
		cacheDir = c.CacheDir()
	}
	cacheRel := cacheDir
	if rel, err := filepath.Rel(root, cacheDir); err == nil && filepath.IsLocal(rel) {
		cacheRel = rel
	}
	return trail.ReadFeedback(trail.FeedbackBases(listCheckouts(ctx, root), cacheRel, w.Since), w)
}

// listCheckouts is root and every other checkout of its repository. A repository whose
// version control cannot list checkouts is root alone.
func listCheckouts(ctx context.Context, root string) []string {
	res, err := vcs.Resolve(ctx, root, "", types.VCSOptions{})
	if err != nil || res.VCS == nil {
		return []string{root}
	}
	lister, ok := res.VCS.(types.CheckoutLister)
	if !ok {
		return []string{root}
	}
	others, err := lister.OtherCheckouts(root)
	if err != nil {
		return []string{root}
	}
	return append([]string{root}, others...)
}

// feedbackWindow reads the trail call's options. An unknown key raises, since a misspelled
// option silently read as its default is a report about the wrong window.
func feedbackWindow(opts map[string]any, now time.Time) (trail.FeedbackWindow, error) {
	known := []string{"session", "since", "until"}
	for k := range opts {
		if !slices.Contains(known, k) {
			return trail.FeedbackWindow{}, fmt.Errorf("trail.read: unknown option %q; options are %s", k, strings.Join(known, ", "))
		}
	}
	text := func(k string) (string, error) {
		v, ok := opts[k]
		if !ok || v == nil {
			return "", nil
		}
		s, ok := v.(string)
		if !ok {
			return "", fmt.Errorf("trail.read: opts.%s is %T, want a string", k, v)
		}
		return strings.TrimSpace(s), nil
	}
	w := trail.FeedbackWindow{Until: now}
	var err error
	if w.Session, err = text("session"); err != nil {
		return w, err
	}
	until, err := text("until")
	if err != nil {
		return w, err
	}
	if until != "" {
		if w.Until, err = time.Parse(time.RFC3339, until); err != nil {
			return w, fmt.Errorf("trail.read: opts.until %q is not an RFC3339 time", until)
		}
	}
	since, err := text("since")
	if err != nil {
		return w, err
	}
	switch since {
	case "":
		w.Since = w.Until.Add(-trailDefaultSince)
	default:
		if d, derr := time.ParseDuration(since); derr == nil && d > 0 {
			w.Since = w.Until.Add(-d)
		} else if t, terr := time.Parse(time.RFC3339, since); terr == nil {
			w.Since = t
		} else {
			return w, fmt.Errorf("trail.read: opts.since %q is neither a positive duration (6h) nor an RFC3339 time", since)
		}
	}
	if !w.Since.Before(w.Until) {
		return w, fmt.Errorf("trail.read: the window starts at %s, not before its end %s", w.Since.Format(time.RFC3339), w.Until.Format(time.RFC3339))
	}
	return w, nil
}

// MagusTrailMarks backs magus\trail.marks: this repository's marks, oldest first.
func MagusTrailMarks(ctx context.Context) ([]types.FeedbackMark, error) {
	dir, err := feedbackMarksDir(ctx, "trail.marks")
	if err != nil {
		return nil, err
	}
	return trail.ReadFeedbackMarks(dir)
}

// MagusTrailMark backs magus\trail.mark: records one mark in this repository's store.
func MagusTrailMark(ctx context.Context, mark map[string]any) (types.FeedbackMark, error) {
	dir, err := feedbackMarksDir(ctx, "trail.mark")
	if err != nil {
		return types.FeedbackMark{}, err
	}
	m, err := decodeFeedbackMark(mark)
	if err != nil {
		return types.FeedbackMark{}, err
	}
	return trail.AppendFeedbackMark(dir, m, time.Now())
}

func feedbackMarksDir(ctx context.Context, member string) (string, error) {
	ws := types.WorkspaceFromContext(ctx)
	if ws == nil {
		return "", errNoWorkspace(member)
	}
	return trail.FeedbackMarksDir(ws.Root())
}

// decodeFeedbackMark reads a FeedbackMark object, raising on a field of the wrong type
// rather than storing its zero value.
func decodeFeedbackMark(in map[string]any) (types.FeedbackMark, error) {
	var m types.FeedbackMark
	str := func(k string) (string, error) {
		v, ok := in[k]
		if !ok || v == nil {
			return "", nil
		}
		s, ok := v.(string)
		if !ok {
			return "", fmt.Errorf("trail.mark: %s is %T, want a string", k, v)
		}
		return s, nil
	}
	var section, verdict string
	var err error
	for k, dst := range map[string]*string{
		"id": &m.ID, "section": &section, "key": &m.Key, "rule": &m.Rule,
		"verdict": &verdict, "note": &m.Note, "session": &m.Session,
	} {
		if *dst, err = str(k); err != nil {
			return m, err
		}
	}
	m.Section, m.Verdict = types.FeedbackSection(section), types.FeedbackVerdict(verdict)
	switch ex := in["examples"].(type) {
	case nil:
	case []any:
		for _, e := range ex {
			s, ok := e.(string)
			if !ok {
				return m, fmt.Errorf("trail.mark: examples holds a %T, want strings", e)
			}
			m.Examples = append(m.Examples, s)
		}
	case []string:
		m.Examples = ex
	default:
		return m, fmt.Errorf("trail.mark: examples is %T, want a list of strings", ex)
	}
	return m, nil
}
