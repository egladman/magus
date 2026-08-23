package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// stubAdviceDir builds an advisor directory holding the REAL advice.buzz plus the given
// stubs, so a test exercises the shipped collect sink rather than a re-implementation of
// it. The stubs stand in for the advisors themselves, whose findings depend on a git
// history and a loaded workspace.
func stubAdviceDir(t *testing.T, stubs map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	src, err := os.ReadFile(filepath.Join("..", "..", adviceDirRel, "advice.buzz"))
	if err != nil {
		t.Fatalf("read advice.buzz: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "advice.buzz"), src, 0o600); err != nil {
		t.Fatalf("write advice.buzz: %v", err)
	}
	for name, body := range stubs {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

// echoAdvisor publishes the local env contract back out, so a test can assert what
// reached the advisor rather than what the driver believes it sent.
const echoAdvisor = `import "advice";
import "std";

fun main() > void !> any {
    std\print("echo: looked at the tree");
    advice\publish(advice\env("REPO"), pr: advice\env("PR_NUMBER"), name: "echo",
        title: advice\env("PR_HEAD_SHA"), body: advice\env("PR_BASE"));
}
`

// warningAdvisor trips BZZ3002 (string rebuilt in a loop) and still publishes, the shape
// of every shipped advisor that lints imperfectly but runs.
const warningAdvisor = `import "advice";

fun main() > void !> any {
    var text = "";
    foreach (part in ["still", "standing"]) {
        text = text + part;
    }
    advice\publish("", pr: "", name: "warned", title: "Ran anyway", body: text);
}
`

const retractingAdvisor = `import "advice";

fun main() > void !> any {
    advice\publish("", pr: "", name: "quiet", title: "Nothing to report", body: "");
}
`

const brokenAdvisor = `fun main() > void !> any {
    throw "broken on purpose";
}
`

// staleBaseAdvisor exercises the half of the local-base contract no Buzz test can reach:
// advice.buzz decides on os\env, and os\with_env does not touch this process's own
// environment, so only a driver that really sets the variable can put an advisor into
// local mode.
//
// The base is one no remote can resolve. In CI mode fetchBase runs the refspec fetch,
// git fails to find the ref, and fetchBase throws - so merely reaching publish is the
// proof that the local-mode gate returned before any git ran.
const staleBaseAdvisor = `import "advice";

fun main() > void !> any {
    advice\fetchBase("magus-advice-no-such-base", refspec: true);
    advice\publish("", pr: "", name: "stale", title: "Stale base",
        body: advice\staleBaseNote(advice\env("PR_BASE")));
}
`

func TestCollectAdviceEmitsSectionsAndSkipsTheForge(t *testing.T) {
	dir := stubAdviceDir(t, map[string]string{"echo.buzz": echoAdvisor})

	sections, notes, err := collectAdvice(context.Background(), dir, []string{"echo.buzz"}, "main")
	if err != nil {
		t.Fatalf("collectAdvice: %v", err)
	}
	if len(notes) != 0 {
		// Compile warnings are notes too, so a note here may be a BZZ diagnostic raised by
		// the shipped advice.buzz rather than anything the stub did.
		t.Fatalf("notes = %v, want none", notes)
	}
	if len(sections) != 1 {
		t.Fatalf("sections = %+v, want exactly one", sections)
	}
	got := sections[0]
	if got.Name != "echo" {
		t.Errorf("Name = %q, want %q", got.Name, "echo")
	}
	// The driver's base reaches the advisor as PR_BASE, which is the whole point of the
	// shared env helper's local mode.
	if got.Body != "main" {
		t.Errorf("Body = %q, want the base %q", got.Body, "main")
	}
	// PR_HEAD_SHA is supplied rather than left empty; an empty one makes every advisor
	// read the run as "not a pull request" and say nothing at all.
	if got.Title == "" {
		t.Error("Title is empty: PR_HEAD_SHA was not supplied to the advisor")
	}
}

func TestCollectAdviceKeepsARetraction(t *testing.T) {
	dir := stubAdviceDir(t, map[string]string{"quiet.buzz": retractingAdvisor})

	sections, _, err := collectAdvice(context.Background(), dir, []string{"quiet.buzz"}, "main")
	if err != nil {
		t.Fatalf("collectAdvice: %v", err)
	}
	if len(sections) != 1 || sections[0].Name != "quiet" || sections[0].Body != "" {
		t.Fatalf("sections = %+v, want one empty-bodied \"quiet\" section", sections)
	}
}

func TestCollectAdviceSurvivesABrokenAdvisor(t *testing.T) {
	dir := stubAdviceDir(t, map[string]string{
		"echo.buzz":   echoAdvisor,
		"broken.buzz": brokenAdvisor,
		"quiet.buzz":  retractingAdvisor,
	})
	// Broken in the MIDDLE: a failure that stops the run would take the third advisor
	// with it and look identical to one that had nothing to say.
	order := []string{"echo.buzz", "broken.buzz", "quiet.buzz"}

	sections, notes, err := collectAdvice(context.Background(), dir, order, "main")
	if err != nil {
		t.Fatalf("collectAdvice: %v", err)
	}
	if len(notes) != 1 {
		t.Fatalf("notes = %v, want exactly one", notes)
	}
	if !strings.Contains(notes[0], "broken.buzz") || !strings.Contains(notes[0], "broken on purpose") {
		t.Errorf("note = %q, want it to name the advisor and the error", notes[0])
	}
	// The stamp is applied here, where a failure is distinguishable from a warning; the
	// renderer prints notes verbatim, so an unstamped failure would read as a warning
	// from an advisor that ran.
	if !strings.HasPrefix(notes[0], "could not run: ") {
		t.Errorf("note = %q, want the could-not-run stamp on a failure", notes[0])
	}
	if len(sections) != 2 {
		t.Fatalf("sections = %+v, want the two working advisors", sections)
	}
	if sections[0].Name != "echo" || sections[1].Name != "quiet" {
		t.Errorf("sections = %+v, want them in the order they were listed", sections)
	}
}

// A warning is not a failure: the advisor ran and published, so its note must not carry
// the could-not-run stamp - ten warnings once rendered as ten dead advisors.
func TestCollectAdviceDoesNotStampAWarningAsAFailure(t *testing.T) {
	dir := stubAdviceDir(t, map[string]string{"warned.buzz": warningAdvisor})

	sections, notes, err := collectAdvice(context.Background(), dir, []string{"warned.buzz"}, "main")
	if err != nil {
		t.Fatalf("collectAdvice: %v", err)
	}
	if len(sections) != 1 {
		t.Fatalf("sections = %+v, want the advisor's finding: it ran", sections)
	}
	if len(notes) == 0 {
		t.Fatal("want the warning surfaced as a note")
	}
	for _, n := range notes {
		if strings.HasPrefix(n, "could not run: ") {
			t.Errorf("note = %q, want no could-not-run stamp on a warning", n)
		}
	}
}

// TestLocalModeReadsAStaleBaseWithoutFetching pins the rule that a local advice run stays
// off the network. `magus diff` is a read-only report on a working tree - it may run
// offline, and under --watch it re-fires on every save - so fetching, and writing
// refs/remotes/ while doing it, is a report mutating what it reports on.
func TestLocalModeReadsAStaleBaseWithoutFetching(t *testing.T) {
	dir := stubAdviceDir(t, map[string]string{"stale.buzz": staleBaseAdvisor})

	sections, notes, err := collectAdvice(context.Background(), dir, []string{"stale.buzz"}, "main")
	if err != nil {
		t.Fatalf("collectAdvice: %v", err)
	}
	// A note here IS the defect: fetchBase reached git, git could not resolve the refspec,
	// and it threw. On the correct path no git runs at all.
	if len(notes) != 0 {
		t.Fatalf("notes = %v, want none: fetchBase went to the network during a local run", notes)
	}
	if len(sections) != 1 {
		t.Fatalf("sections = %+v, want exactly one", sections)
	}
	// Not fetching is only half the contract. A finding measured against a base that may be
	// weeks old is not wrong, but it answers a question about that older base, and the
	// section has to say so or the reader cannot tell.
	body := sections[0].Body
	if !strings.Contains(body, "origin/main") {
		t.Errorf("Body = %q, want it to name the unfetched ref it compared against", body)
	}
	if !strings.Contains(body, "git fetch origin main") {
		t.Errorf("Body = %q, want it to name the command that refreshes the base", body)
	}
}

func TestParseAdviceSectionsDropsProgressLines(t *testing.T) {
	out := "unclaimed: advised on a, b\n" +
		`{"name":"unclaimed","title":"Files no project claims (2)","body":"a\nb"}` + "\n" +
		"not json at all\n" +
		`{"title":"no name","body":"x"}` + "\n"

	got := parseAdviceSections(out)
	if len(got) != 1 {
		t.Fatalf("got %+v, want only the one named section", got)
	}
	if got[0].Body != "a\nb" {
		t.Errorf("Body = %q, want the escaped newline decoded", got[0].Body)
	}
}

func TestSetAdviceEnvRestoresAbsence(t *testing.T) {
	t.Setenv(adviceModeEnv, "")
	if err := os.Unsetenv(adviceModeEnv); err != nil {
		t.Fatalf("unsetenv: %v", err)
	}

	restore, err := setAdviceEnv("main")
	if err != nil {
		t.Fatalf("setAdviceEnv: %v", err)
	}
	if got := os.Getenv(adviceModeEnv); got != adviceModeLocal {
		t.Fatalf("%s = %q, want %q", adviceModeEnv, got, adviceModeLocal)
	}
	restore()
	// Absent, not empty: advice.buzz tells the two apart, so restoring to "" would leave
	// a later reader looking at a variable this call invented.
	if _, ok := os.LookupEnv(adviceModeEnv); ok {
		t.Errorf("%s is still set after restore", adviceModeEnv)
	}
}

// adviceLocalExclusions are read-only advisors action.yml runs that `magus diff`
// deliberately does not. The value is the reason, and carrying one is the point: leaving
// an advisor out is a decision, and a decision with no reason recorded is indistinguishable
// from having forgotten it.
var adviceLocalExclusions = map[string]string{
	"first-contribution.buzz": "reads the pull request's author through its own gh call " +
		"rather than through advice.buzz, so local mode cannot intercept it, and a working " +
		"tree has no first-time contributor to welcome",
}

// adviceStep is one step of the advice composite action, reduced to the two facts this
// test needs: the advisor it runs, and the environment keys it sets.
type adviceStep struct {
	name    string
	file    string
	envKeys map[string]bool
}

// parseAdviceSteps reads the advice composite action and returns its advisor steps in the
// order action.yml declares them.
//
// A LINE SCAN rather than a YAML decode, which is a judgment worth recording. Decoding
// would mean modeling enough of the composite-action schema to reach `runs.steps[].env`
// and `.run`, and `run` would still be a shell string this test has to pick a script path
// out of by hand - so the schema buys nothing and the sub-parse remains either way. It
// would also put a YAML dependency in package main to serve one test. The scan reads the
// same two facts a human reads, off a file whose indentation the action schema fixes:
// steps open at `    - name:`, step keys sit at six spaces, env keys at eight.
//
// The scan is allowed to be wrong in one direction only. A step it fails to recognize
// drops out of the returned set and then surfaces as a mismatch against localAdvisors,
// which is a red test naming the file - never a quietly shorter list.
func parseAdviceSteps(t *testing.T) []adviceStep {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("..", "..", adviceDirRel, "action.yml"))
	if err != nil {
		t.Fatalf("read action.yml: %v", err)
	}

	var steps []adviceStep
	var cur *adviceStep
	inEnv := false
	flush := func() {
		if cur != nil && cur.file != "" {
			steps = append(steps, *cur)
		}
		cur = nil
	}
	for _, line := range strings.Split(string(src), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "    - name: "):
			flush()
			cur = &adviceStep{
				name:    strings.TrimSpace(strings.TrimPrefix(line, "    - name: ")),
				envKeys: map[string]bool{},
			}
			inEnv = false
		case cur == nil:
			// Everything ahead of the first step: the action's description and inputs.
		case trimmed == "" || strings.HasPrefix(trimmed, "#"):
			// Blank lines and comments say nothing about where the scan is.
		case inEnv && strings.HasPrefix(line, "        "):
			if key, _, ok := strings.Cut(trimmed, ":"); ok {
				cur.envKeys[key] = true
			}
		case trimmed == "env:":
			inEnv = true
		case strings.HasPrefix(line, "      run: "):
			inEnv = false
			run := strings.TrimPrefix(line, "      run: ")
			_, after, ok := strings.Cut(run, "$GITHUB_ACTION_PATH/")
			if !ok {
				// A step invoking an advisor some other way would evade the scan
				// entirely, which is the one failure this design cannot absorb.
				if strings.Contains(run, ".buzz") {
					t.Errorf("step %q runs a .buzz script the scan cannot name: %q", cur.name, run)
				}
				continue
			}
			cur.file, _, _ = strings.Cut(after, `"`)
		default:
			inEnv = false
		}
	}
	flush()
	return steps
}

// TestLocalAdvisorsMatchActionYML is the gate on localAdvisors restating action.yml by
// hand. A read-only advisor added to CI that never reaches `magus diff` is invisible
// otherwise: both halves keep working, and the local command is simply quieter than the
// pull request for no stated reason.
func TestLocalAdvisorsMatchActionYML(t *testing.T) {
	steps := parseAdviceSteps(t)

	// A step carrying FIX_LABEL is a WRITER. That variable is the per-change consent the
	// two fixers and the label-settler each read before touching the branch, so it is a
	// structural signal action.yml already carries, rather than a second hand-kept list of
	// writers that could drift exactly the way localAdvisors can. FIX_LABEL_OFFER - which
	// the read-only merge-conflict advisor sets - is a different key and does not match.
	var readOnly []string
	writers := 0
	for _, s := range steps {
		if s.envKeys["FIX_LABEL"] {
			writers++
			continue
		}
		readOnly = append(readOnly, s.file)
	}
	if len(readOnly) == 0 || writers == 0 {
		t.Fatalf("the scan found %d steps, %d read-only and %d writers: it is measuring "+
			"nothing, and every comparison below would pass on an empty file",
			len(steps), len(readOnly), writers)
	}

	want := make([]string, 0, len(readOnly))
	for _, file := range readOnly {
		if _, excluded := adviceLocalExclusions[file]; !excluded {
			want = append(want, file)
		}
	}
	for file := range adviceLocalExclusions {
		if !slices.Contains(readOnly, file) {
			t.Errorf("adviceLocalExclusions names %q, which action.yml no longer runs as a "+
				"read-only advisor: drop the entry, since the exclusion now protects nothing", file)
		}
	}
	if slices.Equal(localAdvisors, want) {
		return
	}

	mismatched := false
	for _, file := range want {
		if !slices.Contains(localAdvisors, file) {
			mismatched = true
			t.Errorf("action.yml runs read-only advisor %q and `magus diff` does not. Add it "+
				"to localAdvisors, or name it in adviceLocalExclusions with the reason it has "+
				"no local meaning. If it WRITES it belongs in neither: a writer is recognized "+
				"here by the FIX_LABEL consent variable on its step, and one that pushes "+
				"without reading that label is a bug in the advisor, not in this test.", file)
		}
	}
	for _, file := range localAdvisors {
		if !slices.Contains(want, file) {
			mismatched = true
			t.Errorf("localAdvisors runs %q, which action.yml does not run as a read-only "+
				"advisor: it was renamed, removed, or has become a writer.", file)
		}
	}
	if !mismatched {
		// Same members either way, so only the order moved. It is not cosmetic: a local
		// reader gets the findings in the order CI chose to present them.
		t.Errorf("localAdvisors = %v, want action.yml's order %v", localAdvisors, want)
	}
}
