package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/interp/bindings"
	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/libs/gopherbuzz"
	vm "github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/types"
)

// adviceSection is one advisor's finding: the section it owns in the pull-request
// comment, rendered for a local reader instead. An EMPTY Body is a retraction - the
// advisor ran and found nothing - and is a section like any other, not an absence.
type adviceSection struct {
	Name  string `json:"name"  yaml:"name"`
	Title string `json:"title" yaml:"title"`
	Body  string `json:"body"  yaml:"body"`
}

// adviceDirRel is where this repository keeps the advisors. They are checked in as a
// composite action because CI is where they run first, not because the pull request is
// the only place their answers are useful.
var adviceDirRel = filepath.Join(".github", "actions", "advice")

// localAdvisors is every advisor a local run may execute, in the order action.yml runs
// them. action.yml is the source of truth for that set; this list restates it.
//
// Restating it rather than reading the directory is deliberate, and the deciding reason
// is safety. Three of the scripts in that directory PUSH to a branch, and nothing about a
// filename separates them from the read-only ones: `fix-generated-drift.buzz` and
// `fix-merge-conflict.buzz` share a prefix that `settle-fix-labels.buzz` does not. A
// sweep of *.buzz would therefore enroll a writer into a local command the moment someone
// added one, and the failure mode of getting that wrong is a `magus diff` that pushes.
//
// Two lesser reasons: the directory also holds `advice.buzz`, which is the shared library
// and not an advisor at all. And filename order is not the order action.yml chose.
//
// first-contribution.buzz is the one read-only advisor deliberately left out: it asks the
// forge who opened the pull request, through its own `gh` call rather than through
// advice.buzz, so local mode cannot intercept it. It also has no local meaning.
//
// Restating is not the same as drifting, and TestLocalAdvisorsMatchActionYML is what keeps
// the two apart: it reads the steps back out of action.yml and fails naming any advisor
// that is in one list and not the other. Adding a read-only advisor to CI without adding
// it here is the failure that gate exists for.
var localAdvisors = []string{
	"merge-conflict.buzz",
	"hand-edited-generated.buzz",
	"target-outputs.buzz",
	"doctor.buzz",
	"version-floor.buzz",
	"unclaimed.buzz",
	"blast-radius.buzz",
	"skip-cache.buzz",
	"conformance.buzz",
	"missing-target.buzz",
	"api-surface.buzz",
}

// runLocalAdvisors runs the read-only PR advisors against the local tree and returns
// their sections in localAdvisors order, plus a note per advisor that failed.
//
// base is a BRANCH name, not a rev: the advisors compare against `origin/<base>`, the
// same way they use PR_BASE in CI.
//
// An advisor that raises produces a note and never an error - one broken advisor must not
// take the other nine down, because the caller is showing a reader what magus knows and
// nine tenths of that is still worth showing. The error return is for a failure that
// makes the whole set meaningless.
//
// Not safe for concurrent use: the advisors read their inputs with os\env, so the two
// local-mode variables are set process-wide for the duration of the call.
func runLocalAdvisors(ctx context.Context, m *magus.Magus, base string) ([]adviceSection, []string, error) {
	dir := filepath.Join(m.Root(), adviceDirRel)
	if _, err := os.Stat(dir); err != nil {
		return nil, []string{fmt.Sprintf("no advisors in this workspace: %s is not readable (%v)", dir, err)}, nil
	}

	// The advisors ask magus about the workspace (magus\describeFile, magus\diff,
	// magus\affectedImpact), which reads it off the context the way `magus buzz` does.
	// The caller's already-loaded workspace is attached rather than loaded again:
	// loadMagus is once-per-process and panics on a second call with a different root.
	if m != nil {
		ctx = types.WithWorkspace(ctx, m)
	}
	return collectAdvice(ctx, dir, localAdvisors, base)
}

// collectAdvice is runLocalAdvisors without the workspace and directory resolution, so a
// test can drive it against stub advisors.
func collectAdvice(ctx context.Context, dir string, files []string, base string) ([]adviceSection, []string, error) {
	restore, err := setAdviceEnv(base)
	if err != nil {
		return nil, nil, err
	}
	defer restore()

	var sections []adviceSection
	var notes []string
	for _, file := range files {
		out, warnings, err := runAdvisor(ctx, dir, file)
		// Sections printed before the failure are kept: an advisor that publishes and
		// then dies has already said something true, and dropping it would report the
		// finding as absent rather than as partial.
		sections = append(sections, parseAdviceSections(out)...)
		// Warnings first, then the failure: that is the order they happened in, and a
		// BZZ3001 above a crash is usually the explanation for it.
		for _, w := range warnings {
			notes = append(notes, fmt.Sprintf("%s: %s", file, w))
		}
		if err != nil {
			// Classified here rather than at render time: warnings and failures share
			// one ordered stream, and only this frame knows which is which. An advisor
			// with ten warnings still ran; painting them all "could not run" reports
			// ten dead advisors that each published a finding.
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
// couples its copy to this one - rename one side alone and every advisor fails with
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

// runAdvisor evaluates one advisor in-process and returns what it printed, the warnings
// its compilation raised, and the error that ended it. Nothing here writes or exits: all
// three are the caller's to report.
//
// The session is built as `magus buzz <file>` builds one - same module surface, same
// strict parse mode, warnings drained at the same point, `fun main() > int` read rather
// than discarded - so an advisor cannot behave one way in CI and another way here. Two
// differences remain, both deliberate:
//
//   - std.print goes to a buffer, not stdout. That IS the transport: a section is a line
//     the advisor printed, and parseAdviceSections reads them back.
//   - the include path is set on the session rather than through BUZZ_INCLUDE_PATH, for
//     the reason at the call site below.
//
// Where those two observations LAND differs as well, and has to. `magus buzz` prints
// warnings to stderr and exits with main's value; ten advisors run here, and the ninth
// failing is not a verdict on `magus diff`. Both become notes instead.
func runAdvisor(ctx context.Context, dir, file string) (string, []string, error) {
	src, err := os.ReadFile(filepath.Join(dir, file))
	if err != nil {
		return "", nil, err
	}
	sess := buzz.NewSession(ctx)
	defer func() { _ = sess.Close() }()
	// `import "advice"` resolves against this list. The composite action gets the same
	// effect from BUZZ_INCLUDE_PATH; setting it directly keeps the advisor's view of the
	// filesystem out of the process environment.
	sess.SetIncludeDirs([]string{dir})

	var out bytes.Buffer
	bindings.RegisterModuleSurface(ctx, sess, bindings.WithScriptOutput(&out))
	bindings.RegisterMagusNamespace(ctx, sess)
	bindings.RegisterSpellSourceModules(sess)

	if err := sess.Exec(ctx, string(src)); err != nil {
		return out.String(), nil, err
	}
	// Drained where `magus buzz` drains them: after Exec, before main. These are parse and
	// check diagnostics (BZZ3001 unused import, and the rest), so Exec is where all of them
	// are produced, and it never fails on one - which is exactly why they need collecting
	// rather than trusting a green run. An advisor whose imports have rotted is the kind of
	// thing a reader wants told, not left to read as an advisor with nothing to say.
	var warnings []string
	for _, w := range sess.Warnings() {
		warnings = append(warnings, w.String())
	}

	mainFn := sess.GetGlobal("main")
	if !mainFn.IsFun() {
		return out.String(), warnings, fmt.Errorf("no main() to run")
	}
	ret, err := sess.CallValue(ctx, mainFn, []vm.Value{vm.ListValue(nil)})
	if err != nil {
		return out.String(), warnings, err
	}
	// `fun main() > int` is upstream's exit-status convention, and the checker permits it,
	// so an advisor may report failure by returning rather than by throwing. Discarding the
	// value read that advisor as having succeeded.
	if ret.IsInt() && ret.AsInt() != 0 {
		return out.String(), warnings, fmt.Errorf("main() returned %d", ret.AsInt())
	}
	return out.String(), warnings, nil
}

// parseAdviceSections picks the section objects out of one advisor's output. An advisor
// also prints a progress line for a human ("unclaimed: advised on ..."), so the two are
// told apart by decoding rather than by position: a line that is not a section object is
// the advisor talking, and is dropped.
func parseAdviceSections(out string) []adviceSection {
	var got []adviceSection
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var s adviceSection
		if err := json.Unmarshal([]byte(line), &s); err != nil || s.Name == "" {
			continue
		}
		got = append(got, s)
	}
	return got
}
