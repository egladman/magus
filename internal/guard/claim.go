package guard

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// gradeClaimedDeclarations grades a write the acting lease me may make at the path level
// by the declarations it changes, when another owning lease claims a declaration of rel.
//
// The payload's edit is applied to the file in memory and the changed lines are placed by
// the same RegionReporter the footprint uses, so the declaration named here is the one
// `magus job wait` will report. A write landing in a declaration another lease claims, and
// none of me's own claims names, is denied. Everything magus cannot compute stands down to
// the path-level verdict: a payload with no edit in it (a whole-file write), an edit that
// does not apply to the file on disk, a file with no driver, a VCS that places nothing.
//
// Nothing is read unless another lease claims a declaration of rel, so a write to a file
// nobody claims below the whole pays for one scan of the rows and no process.
func gradeClaimedDeclarations(ctx context.Context, workspace string, me types.Job, owners []types.Job, rel string, fields writeFields) writeGrade {
	now := time.Now().Unix()
	var others []types.Job
	for _, u := range owners {
		if u.ID != me.ID && !u.Overdue(now) && len(claimsOn(u.WritePaths, rel)) > 0 {
			others = append(others, u)
		}
	}
	if len(others) == 0 {
		return writeGrade{}
	}
	mine := job.ClaimedDeclarations(me.WritePaths, rel)
	if mine == nil {
		// me covers rel whole, or not at all: the path verdict already said which.
		return writeGrade{}
	}
	abs := filepath.Join(workspace, filepath.FromSlash(rel))
	before, err := os.ReadFile(abs)
	if err != nil {
		return writeGrade{}
	}
	after, ok := applyEdits(string(before), fields)
	if !ok {
		return writeGrade{}
	}
	res, err := vcs.Resolve(ctx, workspace, "", types.VCSOptions{})
	if err != nil || res.VCS == nil {
		return writeGrade{}
	}
	regions, err := res.VCS.RegionsBetween(ctx, workspace, rel, before, []byte(after))
	if err != nil {
		return writeGrade{}
	}
	for _, r := range regions {
		loc := r.Location()
		if loc.Declaration == "" || loc.Declaration == types.Preamble || namedBy(mine, loc.Declaration) {
			continue
		}
		for _, owner := range others {
			if claim, ok := namingClaim(claimsOn(owner.WritePaths, rel), loc.Declaration); ok {
				return denyClaimedDeclaration(me, owner, rel, claim, loc.Declaration, mine)
			}
		}
	}
	return writeGrade{}
}

func denyClaimedDeclaration(me, owner types.Job, rel, claim, declaration string, mine []string) writeGrade {
	return writeGrade{Decision: "deny", Rule: string(denyRuleClaimedDeclaration), Reason: fmt.Sprintf(
		"magus workspace: edit inside the declarations you claim in %s (%s). "+leaseActorClause("re-partition the plan, or release "+rel+"#"+claim+" once lease "+owner.ID+" has finished with it")+"\n"+
			"This edit changes %s in %s, which lease %s (%s) claims as %s#%s and is %s right now, and you are lease %s. Two agents editing one declaration is the collision a claim exists to prevent; this guard is where it gets read.\n"+
			"Lease %s was last updated %s ago. If nobody holds it any more, `%s` releases its paths; magus never ends a row on its own.",
		rel, strings.Join(mine, ", "),
		declaration, rel, owner.ID, criteriaLine(owner), rel, claim, owner.State, me.ID,
		owner.ID, time.Since(time.Unix(owner.Updated, 0)).Round(time.Second), hint.JobExit.With(owner.ID))}
}

// claimsOn are the declarations write paths claim in the file rel, whole-file entries left
// out: a lease covering rel whole claims no declaration of it in particular.
func claimsOn(writePaths []string, rel string) []string {
	var out []string
	for _, entry := range writePaths {
		if p, decl := types.SplitClaim(entry); decl != "" && p != "" && path.Clean(p) == rel {
			out = append(out, decl)
		}
	}
	return out
}

func namedBy(claims []string, declaration string) bool {
	_, ok := namingClaim(claims, declaration)
	return ok
}

func namingClaim(claims []string, declaration string) (string, bool) {
	i := slices.IndexFunc(claims, func(c string) bool { return types.NamesDeclaration(c, declaration) })
	if i < 0 {
		return "", false
	}
	return claims[i], true
}

// applyEdits is the file a write's replacements leave behind, reporting false when the
// payload carries none or one would not apply: a replacement whose text is absent, or
// present more than once without ReplaceAll, is one the host refuses too.
func applyEdits(body string, fields writeFields) (string, bool) {
	edits := fields.Edits
	if len(edits) == 0 && fields.OldText != "" {
		edits = []textEdit{{OldText: fields.OldText, NewText: fields.NewText, ReplaceAll: fields.ReplaceAll}}
	}
	if len(edits) == 0 {
		return "", false
	}
	for _, e := range edits {
		switch n := strings.Count(body, e.OldText); {
		case e.OldText == "" || n == 0:
			return "", false
		case e.ReplaceAll:
			body = strings.ReplaceAll(body, e.OldText, e.NewText)
		case n > 1:
			return "", false
		default:
			body = strings.Replace(body, e.OldText, e.NewText, 1)
		}
	}
	return body, true
}
