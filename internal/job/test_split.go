package job

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/types"
)

// testWorkerReason is the worker.reason a split test row carries.
const testWorkerReason = "edits tests that exist at the base, apart from the worker whose change they grade"

// existingTests is what a row writes in one directory. unplaced are the test files no
// diff driver placed.
type existingTests struct {
	code     []string
	claims   []string
	unplaced []string
}

// existingTestProblems is one WritePathExistingTests per directory where row writes code
// and test declarations that exist at its base. New tests are not one. A worker editing
// the test that grades its own change can weaken it.
func existingTestProblems(ctx context.Context, store *Store, row job.Job) types.JobProblems {
	if store == nil || store.root == "" || row.ReadOnly || row.Integrating() {
		return nil
	}
	root := store.root
	files := slices.DeleteFunc(writtenFiles(root, row), func(f string) bool { return slices.Contains(row.Paths.Soft, f) })
	byDir := map[string]*existingTests{}
	at := func(dir string) *existingTests {
		if byDir[dir] == nil {
			byDir[dir] = &existingTests{}
		}
		return byDir[dir]
	}
	var tests []string
	for _, f := range files {
		switch {
		case !types.IsTestPath(f):
			at(path.Dir(f)).code = append(at(path.Dir(f)).code, f)
		case isRegularFile(root, f):
			tests = append(tests, f)
		}
	}
	tests = slices.DeleteFunc(tests, func(f string) bool { return byDir[path.Dir(f)] == nil })
	if len(tests) == 0 {
		return nil
	}
	driver, drivers, driverErr := claimDrivers(ctx, root, tests)
	for _, f := range tests {
		e := at(path.Dir(f))
		var all []string
		placed := driverErr == nil && drivers[f] != ""
		if placed {
			all, placed = readDeclarations(ctx, driver, root, f)
		}
		claims, whole := editableTests(row, f, all, placed)
		e.claims = append(e.claims, claims...)
		if whole {
			e.unplaced = append(e.unplaced, f)
		}
	}
	var dirs, claims []string
	for dir, e := range byDir {
		if len(e.code) > 0 && len(e.claims) > 0 {
			dirs = append(dirs, dir)
			claims = append(claims, e.claims...)
		}
	}
	if len(dirs) == 0 {
		return nil
	}
	slices.Sort(dirs)
	slices.Sort(claims)
	fix := testSplitFix(row, claims)
	var ps types.JobProblems
	for _, dir := range dirs {
		e := byDir[dir]
		msg := fmt.Sprintf("%s writes the code of %s (%s) and tests that exist at its base (%s): its worker could weaken the test that grades its own change",
			row.ID, dir, strings.Join(e.code, ", "), strings.Join(e.claims, ", "))
		if len(e.unplaced) > 0 {
			msg += fmt.Sprintf("; no diff driver placed the declarations of %s, so the split holds it whole and new tests go in a new file", strings.Join(e.unplaced, ", "))
		}
		deviation, _ := codec.Marshal(job.Deviation{Code: job.Code(types.WritePathExistingTests), Subject: dir, Reason: "<why one worker edits this code and its tests>"})
		ps = append(ps, types.JobProblem{
			Code:    types.WritePathExistingTests,
			Field:   "paths.write",
			Subject: dir,
			Message: msg,
			Hint:    fmt.Sprintf("%s; or keep them in one row with the deviation %s", fix, deviation),
		})
	}
	return ps
}

// editableTests is the claims on the existing test file f that row may edit, one per
// declaration of all. Unplaced declarations cannot be told apart, so it is then the row's
// own claims or f whole.
func editableTests(row job.Job, f string, all []string, placed bool) (claims []string, whole bool) {
	claimed := ClaimedDeclarations(row, f)
	if claimed != nil && len(claimed) == 0 {
		return nil, false
	}
	if !placed {
		if claimed == nil {
			return []string{f}, true
		}
		for _, c := range claimed {
			claims = append(claims, fmt.Sprintf("%s#%s", f, c))
		}
		return claims, true
	}
	denied := DeniedDeclarations(row.Paths.Deny, f)
	names := func(list []string, d string) bool {
		return slices.ContainsFunc(list, func(c string) bool { return job.NamesDeclaration(c, d) })
	}
	for _, d := range all {
		if claimed != nil && !names(claimed, d) || names(denied, d) {
			continue
		}
		word := claimWord(d, all)
		if len(job.NamedDeclarations(word, all)) != 1 {
			word = d
		}
		claims = append(claims, fmt.Sprintf("%s#%s", f, word))
	}
	return claims, false
}

// splitRow is the part of a row a split fix prints.
type splitRow struct {
	ID        string     `json:"id"`
	DependsOn []string   `json:"depends_on,omitempty"`
	Paths     splitPaths `json:"paths"`
	Agent     *job.Agent `json:"agent,omitempty"`
}

type splitPaths struct {
	Write []string `json:"write"`
	Deny  []string `json:"deny,omitempty"`
}

// testSplitFix is the two rows that replace row: row itself denying claims, and a row a new
// worker takes for claims alone. That row depends on row, so its worker reads the
// implementation it grades.
func testSplitFix(row job.Job, claims []string) string {
	deny := slices.Clone(row.Paths.Deny)
	for _, c := range claims {
		if !slices.Contains(deny, c) {
			deny = append(deny, c)
		}
	}
	impl, _ := codec.Marshal(splitRow{ID: row.ID, Paths: splitPaths{Write: row.Paths.Write, Deny: deny}})
	tests, _ := codec.Marshal(splitRow{
		ID:        row.ID + "-tests",
		DependsOn: []string{row.ID},
		Paths:     splitPaths{Write: claims},
		Agent:     &job.Agent{Worker: job.Worker{Type: job.WorkerTypeNew, Reason: testWorkerReason}},
	})
	return fmt.Sprintf("split the existing tests into their own row: %s, then %s", impl, tests)
}

func isRegularFile(root, rel string) bool {
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
	return err == nil && info.Mode().IsRegular()
}
