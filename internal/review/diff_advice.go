package review

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/egladman/magus/internal/interp/bindings"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types/diff"
)

// AdviceSection is one advisor's finding: the section it owns in the pull-request
// comment, rendered for a local reader instead. An EMPTY Body is a retraction (the
// advisor ran and found nothing) and is a section like any other, not an absence.
type AdviceSection struct {
	Name  string `json:"name"  yaml:"name"`
	Title string `json:"title" yaml:"title"`
	Body  string `json:"body"  yaml:"body"`
}

// adviceDirRel is where this repository keeps the advisors. They are checked in as a
// composite action because CI is where they run first, not because the pull request is
// the only place their answers are useful.
var adviceDirRel = filepath.Join(".github", "actions", "advice")

// localAdvisors is every advisor a local run may execute, in the order the action's entry
// script (advise.buzz) runs them. advise.buzz is the source of truth for that set; this
// list restates it.
//
// Restating it rather than reading the directory is deliberate: the directory also holds
// `advice.buzz` and `advise.buzz`, the shared library and the entry script, neither an
// advisor, and filename order is not the order advise.buzz chose.
//
// first-contribution.buzz, merge-queue.buzz and trusted-script-moved.buzz are the read-only
// advisors deliberately left out: each asks the forge about the pull request through its
// own `gh` call, and none has a local meaning. adviceLocalExclusions in the test records why.
//
// Restating is not the same as drifting, and TestLocalAdvisorsMatchAdviseBuzz is what keeps
// the two apart: it reads the lists back out of advise.buzz and fails naming any advisor
// that is in one list and not the other. Adding a read-only advisor to CI without adding
// it here is the failure that gate exists for.
var localAdvisors = []string{
	"merge-conflict.buzz",
	"doctor.buzz",
	"version-floor.buzz",
	"unclaimed.buzz",
	"blast-radius.buzz",
	"skip-cache.buzz",
	"conformance.buzz",
	"missing-target.buzz",
	"exported-api.buzz",
	"change-risk.buzz",
}

// LocalAdvice runs the read-only PR advisors against the local tree and returns
// their sections in localAdvisors order, plus a note per advisor that failed.
//
// base is a BRANCH name, not a rev: the advisors compare against `origin/<base>`, the
// same way they use the pull request's base branch in CI.
//
// An advisor that raises produces a note and never an error: one broken advisor must not
// take the other nine down, because the caller is showing a reader what magus knows and
// nine tenths of that is still worth showing. The error return is for a failure that
// makes the whole set meaningless.
//
// Not safe for concurrent use: the advisors read their inputs with os\env, so the two
// local-mode variables are set process-wide for the duration of the call.
//
// ctx carries the workspace, which the advisors read off it as `magus buzz` does. rev is the
// review this run already computed. It is handed to the advisors that read one
// (advice.buzz's review, through adviceReviewEnv), so none of them diffs the tree again.
func LocalAdvice(ctx context.Context, root, base string, rev diff.Diff) ([]AdviceSection, []string, error) {
	dir := filepath.Join(root, adviceDirRel)
	if _, err := os.Stat(dir); err != nil {
		return nil, []string{fmt.Sprintf("no advisors in this workspace: %s is not readable (%v)", dir, err)}, nil
	}
	restore, err := shareReview(rev)
	if err != nil {
		return nil, nil, err
	}
	defer restore()

	return collectAdvice(ctx, dir, localAdvisors, base)
}

// collectAdvice is LocalAdvice without the workspace and directory resolution, so a
// test can drive it against stub advisors.
func collectAdvice(ctx context.Context, dir string, files []string, base string) ([]AdviceSection, []string, error) {
	restore, err := setAdviceEnv(base)
	if err != nil {
		return nil, nil, err
	}
	defer restore()

	var sections []AdviceSection
	var notes []string
	for _, file := range files {
		out, warnings, err := bindings.RunScriptMain(ctx, dir, file)
		// Sections printed before the failure are kept: an advisor that publishes and
		// then dies has already said something true, and dropping it would report the
		// finding as absent rather than as partial.
		sections = append(sections, parseAdviceSections(out)...)
		if err != nil {
			// Warnings first, then the failure: that is the order they happened in, and a
			// BZZ3001 above a crash is usually the explanation for it.
			//
			// ONLY when it crashed. These are lint diagnostics about the advisor's own
			// source (magus's shipped scripts, not the reader's change), so printing them
			// unconditionally buries a one-line docs fix under tens of lines about magus's
			// own files, which reads as "you broke something". That lint belongs in magus's
			// own lint run.
			for _, w := range warnings {
				notes = append(notes, fmt.Sprintf("%s: %s", file, w))
			}
			// Stamped here, not at render time: warnings and failures share one ordered
			// stream, and only this frame knows which is which.
			notes = append(notes, fmt.Sprintf("could not run: %s: %v", file, err))
		}
	}
	return sections, notes, nil
}

// The contract between this driver and advice.buzz. The names are read there with os\env;
// there is no per-session environment to hand a Buzz script, so they are set on the
// process.
//
// MAGUS_INTERNAL_ says what these are: the handshake between two halves of one feature,
// not a knob. Setting either by hand puts the advisors into local mode with no driver
// reading their stdout, and both names may change with this file in one commit. advice.buzz
// pins the same three strings in a test block of its own, because nothing at runtime
// couples its copy to this one; rename one side alone and every advisor fails with
// "nowhere to publish" instead of saying anything.
const (
	adviceModeEnv       = "MAGUS_INTERNAL_ADVICE_MODE"
	adviceModeLocal     = "local"
	adviceBaseBranchEnv = "MAGUS_INTERNAL_ADVICE_BASE_BRANCH"
)

// setAdviceEnv puts the local-mode variables on the process and returns the restore.
// It restores an absent variable to absent rather than to empty, because advice.buzz
// distinguishes the two.
func setAdviceEnv(base string) (func(), error) {
	saved := map[string]*string{}
	for name, want := range map[string]string{adviceModeEnv: adviceModeLocal, adviceBaseBranchEnv: base} {
		if had, ok := os.LookupEnv(name); ok {
			saved[name] = &had
		} else {
			saved[name] = nil
		}
		if err := os.Setenv(name, want); err != nil {
			return func() {}, fmt.Errorf("magus diff: set %s: %w", name, err)
		}
	}
	return func() {
		for name, was := range saved {
			if was == nil {
				_ = os.Unsetenv(name)
				continue
			}
			_ = os.Setenv(name, *was)
		}
	}, nil
}

// adviceReviewEnv names the file holding the run's one review.
// advice.buzz's review reads it in place of a fresh magus\change().diff.
// The action's review step sets the same name on the forge.
const adviceReviewEnv = "ADVICE_REVIEW"

// shareReview writes rev where the advisors read it and returns the restore, which removes
// the file and puts the variable back as it was.
func shareReview(rev diff.Diff) (func(), error) {
	f, err := os.CreateTemp("", "magus-advice-review-*.json")
	if err != nil {
		return func() {}, fmt.Errorf("magus diff: save the review for the advisors: %w", err)
	}
	werr := json.NewEncoder(f).Encode(rev)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(f.Name())
		return func() {}, fmt.Errorf("magus diff: save the review for the advisors: %w", werr)
	}
	had, set := os.LookupEnv(adviceReviewEnv)
	if err := os.Setenv(adviceReviewEnv, f.Name()); err != nil {
		_ = os.Remove(f.Name())
		return func() {}, fmt.Errorf("magus diff: set %s: %w", adviceReviewEnv, err)
	}
	return func() {
		if set {
			_ = os.Setenv(adviceReviewEnv, had)
		} else {
			_ = os.Unsetenv(adviceReviewEnv)
		}
		_ = os.Remove(f.Name())
	}, nil
}

// parseAdviceSections picks the section objects out of one advisor's output. An advisor
// also prints a progress line for a human ("unclaimed: advised on ..."), so the two are
// told apart by decoding rather than by position: a line that is not a section object is
// the advisor talking, and is dropped.
func parseAdviceSections(out string) []AdviceSection {
	var got []AdviceSection
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var s AdviceSection
		if err := json.Unmarshal([]byte(line), &s); err != nil || s.Name == "" {
			continue
		}
		got = append(got, s)
	}
	return got
}
