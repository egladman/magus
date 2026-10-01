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

//go:generate go run ../cmd/magus-utils bindings -module feedback -lang buzz -out ../internal/interp/bindings/gen/feedback.go

func init() { Register(Feedback) }

// feedbackDefaultSince is the window a trail read covers when the caller names none: long
// enough for a working day's session, short enough that the trails it stats are few.
const feedbackDefaultSince = 24 * time.Hour

// Feedback is the "magus/feedback" host module: what the agent guard recorded about one
// session, and the verdicts a person gives its rows. hack/dev/show-feedback.buzz renders
// the report; mark-feedback and count-feedback write and fold the verdicts.
//
// A module of its own rather than more magus\ members because it reads two stores no
// other member reads (every checkout's activity trail, and the per-repository marks) and
// writes one of them.
var Feedback = Module{
	Name: "feedback",
	Path: "magus/feedback",
	Doc:  "One agent session's guard record (every call the guard judged, with its verdict, served nexts and command shape, plus the subagents it started) and a person's verdicts on the report's rows, kept per repository.",
	Methods: []Method{
		{
			Name: "trail",
			Doc: "One session's guard record inside a window: {session, host, since, until, checkouts, observations, spawns}. " +
				"opts.session picks the session; omitted, it is the session with the newest agent observation in the window. " +
				"opts.since is a duration back from opts.until (`6h`) or an RFC3339 time, default 24h; opts.until is an RFC3339 time, default now. " +
				"Reads the trail of every checkout of this repository that changed inside the window, because a session's hooks record into the checkout they ran from, which is rarely the worker's. " +
				"An unknown option or an unreadable time raises. Reads the workspace on the context; raises MGS1022 outside one.",
			Args:    []Arg{{Name: "opts", Type: TypeAnyMap, Optional: true}},
			Returns: []Ret{{Type: TypeAny, Object: "FeedbackTrail"}},
			Raises:  true,
			Impl:    FeedbackTrail,
		},
		{
			Name:    "shape",
			Doc:     "A shell line with its paths, patterns and literals normalized away, so calls differing only in what they name read alike: `grep -rn foo src` and `grep -rn bar lib` are both `grep -rn <arg>`. Programs, flags, operators and redirections stay. \"\" for a line the shell parser cannot read.",
			Args:    []Arg{{Name: "command", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    FeedbackShape,
		},
		{
			Name:    "shapes",
			Doc:     "The shape of each program a shell line runs, in order, each with its own redirections: `cd x && grep -rn foo src | head -5` is [`cd <path>`, `grep -rn <arg>`, `head -<n>`]. Programs inside a loop or a command substitution count. Empty for a line the shell parser cannot read.",
			Args:    []Arg{{Name: "command", Type: TypeString}},
			Returns: []Ret{{Type: TypeStringSlice}},
			Impl:    FeedbackShapes,
		},
		{
			Name:    "marks",
			Doc:     "Every verdict a person recorded on a feedback row in this repository, oldest first; a later mark on an id supersedes an earlier one. Kept per repository identity, so every checkout reads the same marks. Raises on a store line that does not decode, and MGS1022 outside a workspace.",
			Returns: []Ret{{Type: TypeAny, Object: "[FeedbackMark]"}},
			Raises:  true,
			Impl:    FeedbackMarks,
		},
		{
			Name: "mark",
			Doc: "Record a person's verdict on one feedback row and return it as stored, its time stamped. " +
				"id is the row's stable id (fb and 12 hex digits), section one of refused, advised, unguarded, next-not-taken, verdict one of should-deny, should-advise, wrong-deny, fine; key names the rule or shape the row groups by. " +
				"Appends to the per-repository store and rewrites nothing. Raises on a malformed mark, an unwritable store, and MGS1022 outside a workspace.",
			Args:    []Arg{{Name: "mark", Type: TypeAnyMap, Object: "FeedbackMark"}},
			Returns: []Ret{{Type: TypeAny, Object: "FeedbackMark"}},
			Raises:  true,
			Impl:    FeedbackMark,
		},
	},
}

// cacheDirer is the one capability of the loaded workspace the trail read needs beyond
// its root: where its cache, and so its trail, lives.
type cacheDirer interface {
	CacheDir() string
}

// FeedbackTrail reads one session's guard record from every checkout's trail.
func FeedbackTrail(ctx context.Context, opts map[string]any) (types.FeedbackTrail, error) {
	ws := types.WorkspaceFromContext(ctx)
	if ws == nil {
		return types.FeedbackTrail{}, errNoWorkspace("feedback.trail")
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
	return trail.ReadFeedback(trail.FeedbackBases(checkoutsOf(ctx, root), cacheRel, w.Since), w)
}

// checkoutsOf is root and every other checkout of its repository. A repository whose
// version control cannot list checkouts is root alone.
func checkoutsOf(ctx context.Context, root string) []string {
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
			return trail.FeedbackWindow{}, fmt.Errorf("feedback.trail: unknown option %q; options are %s", k, strings.Join(known, ", "))
		}
	}
	text := func(k string) (string, error) {
		v, ok := opts[k]
		if !ok || v == nil {
			return "", nil
		}
		s, ok := v.(string)
		if !ok {
			return "", fmt.Errorf("feedback.trail: opts.%s is %T, want a string", k, v)
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
			return w, fmt.Errorf("feedback.trail: opts.until %q is not an RFC3339 time", until)
		}
	}
	since, err := text("since")
	if err != nil {
		return w, err
	}
	switch since {
	case "":
		w.Since = w.Until.Add(-feedbackDefaultSince)
	default:
		if d, derr := time.ParseDuration(since); derr == nil && d > 0 {
			w.Since = w.Until.Add(-d)
		} else if t, terr := time.Parse(time.RFC3339, since); terr == nil {
			w.Since = t
		} else {
			return w, fmt.Errorf("feedback.trail: opts.since %q is neither a positive duration (6h) nor an RFC3339 time", since)
		}
	}
	if !w.Since.Before(w.Until) {
		return w, fmt.Errorf("feedback.trail: the window starts at %s, not before its end %s", w.Since.Format(time.RFC3339), w.Until.Format(time.RFC3339))
	}
	return w, nil
}

// FeedbackShape normalizes a shell line into its shape; see trail.CommandShape.
func FeedbackShape(_ context.Context, command string) (string, error) {
	return trail.CommandShape(command), nil
}

// FeedbackShapes is the shape of each program a shell line runs; see trail.CommandShapes.
func FeedbackShapes(_ context.Context, command string) ([]string, error) {
	return trail.CommandShapes(command), nil
}

// FeedbackMarks reads this repository's feedback marks.
func FeedbackMarks(ctx context.Context) ([]types.FeedbackMark, error) {
	dir, err := feedbackMarksDir(ctx, "feedback.marks")
	if err != nil {
		return nil, err
	}
	return trail.ReadFeedbackMarks(dir)
}

// FeedbackMark records one mark in this repository's store.
func FeedbackMark(ctx context.Context, mark map[string]any) (types.FeedbackMark, error) {
	dir, err := feedbackMarksDir(ctx, "feedback.mark")
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
			return "", fmt.Errorf("feedback.mark: %s is %T, want a string", k, v)
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
				return m, fmt.Errorf("feedback.mark: examples holds a %T, want strings", e)
			}
			m.Examples = append(m.Examples, s)
		}
	case []string:
		m.Examples = ex
	default:
		return m, fmt.Errorf("feedback.mark: examples is %T, want a list of strings", ex)
	}
	return m, nil
}
