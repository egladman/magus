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
// The payload's edit is applied to the file in memory, or a whole-file write's content is
// taken as the file it leaves, and the changed lines are placed by the same RegionReporter
// the footprint uses, so the declaration named here is the one `magus job wait` will
// report. A write landing in a declaration another lease claims, and none of me's own
// claims names, is denied; a whole-file write that deletes or rewrites such a declaration
// lands in it as much as an edit does. Everything magus cannot compute stands down to the
// path-level verdict: an edit that does not apply to the file on disk, a file with no
// driver, a VCS that places nothing.
//
// Nothing is read unless another lease claims a declaration of rel, so a write to a file
// nobody claims below the whole pays for one scan of the rows and no process.
func gradeClaimedDeclarations(ctx context.Context, workspace string, me types.Job, owners []types.Job, rel string, fields writeFields) writeGrade {
	now := time.Now().Unix()
	var others []types.Job
	for _, u := range owners {
		if u.ID != me.ID && !u.Overdue(now) && len(claimsOn(u.HardPaths(), rel)) > 0 {
			others = append(others, u)
		}
	}
	if len(others) == 0 {
		return writeGrade{}
	}
	mine := job.ClaimedDeclarations(me, rel)
	if mine == nil {
		// me covers rel whole, or not at all: the path verdict already said which.
		return writeGrade{}
	}
	abs := filepath.Join(workspace, filepath.FromSlash(rel))
	before, err := os.ReadFile(abs)
	if err != nil {
		return writeGrade{}
	}
	after, ok := resolvedWriteContent(abs, fields)
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
			if claim, ok := namingClaim(claimsOn(owner.HardPaths(), rel), loc.Declaration); ok {
				return denyClaimedDeclaration(me, owner, rel, claim, loc.Declaration, mine)
			}
		}
	}
	return writeGrade{}
}

// gradeConfigNarrowing narrows me's hold on rel, a project's magusfile or magus.yaml its
// write paths cover whole only through a pattern (`**/*.buzz`), to the declarations this write changes: they
// become claims on me's row and the rest of the file is released with its digest (see
// job.NarrowConfigHold), so other jobs may claim what me never touched. It runs on every
// such write and narrows only for a declaration me does not claim yet. A change to a
// declaration another lease claims is denied as gradeClaimedDeclarations denies one.
// A write magus cannot place, and a dry run, narrow nothing and keep the path verdict.
func gradeConfigNarrowing(ctx context.Context, at location, checkout string, me types.Job, owners []types.Job, rel string, fields writeFields) writeGrade {
	// A name check first: job.NarrowConfigHold asks the tree, which no other write should pay for.
	switch {
	case path.Base(rel) == "magusfile.buzz", path.Base(rel) == "magus.yaml", path.Base(path.Dir(rel)) == "magusfiles":
	default:
		return writeGrade{}
	}
	if !coveredOnlyByPattern(me, rel) {
		return writeGrade{}
	}
	changed, placed := changedDeclarations(ctx, checkout, rel, fields)
	if !placed {
		return writeGrade{}
	}
	now := time.Now().Unix()
	mine := claimsOn(me.WritePaths, rel)
	var touched []string
	for _, decl := range changed {
		if decl == types.Preamble || namedBy(mine, decl) || slices.Contains(touched, decl) {
			continue
		}
		for _, owner := range owners {
			if owner.ID == me.ID || owner.Overdue(now) {
				continue
			}
			if claim, ok := namingClaim(claimsOn(owner.HardPaths(), rel), decl); ok {
				return denyClaimedDeclaration(me, owner, rel, claim, decl, mine)
			}
		}
		touched = append(touched, decl)
	}
	if len(touched) == 0 || isDryRun(ctx) {
		return writeGrade{}
	}
	loc := at.jobs()
	loc.Root = checkout
	row, err := job.NarrowConfigHold(ctx, job.NewStore(loc), me.ID, rel, touched)
	if err != nil {
		return writeGrade{}
	}
	return writeGrade{Decision: decisionAdvise,
		Context: fmt.Sprintf("magus workspace: lease %s now holds %s by %s; the rest of the file is released for other jobs to claim.",
			me.ID, rel, quotedClaims(rel, claimsOn(row.WritePaths, rel))),
		Why: fmt.Sprintf("Your write paths cover %s, a project's configuration, only through a pattern. Every job that changes the workspace's configuration writes that file, so magus narrows the hold to the declarations you change, on the first write to each. `magus job wait` grades your changes to it against these claims.", rel)}
}

// coveredOnlyByPattern reports whether a pattern among me's whole-file write paths covers
// rel and no literal one does.
func coveredOnlyByPattern(me types.Job, rel string) bool {
	byPattern := false
	for _, entry := range me.WritePaths {
		p, decl := types.SplitClaim(entry)
		if decl != "" {
			continue
		}
		if _, covered, _ := declarationCovering([]string{entry}, rel); !covered {
			continue
		}
		if !strings.ContainsAny(p, "*?[{") {
			return false
		}
		byPattern = true
	}
	return byPattern
}

func denyClaimedDeclaration(me, owner types.Job, rel, claim, declaration string, mine []string) writeGrade {
	return writeGrade{Decision: decisionDeny, Rule: string(denyRuleClaimedDeclaration), Reason: denial{
		Say: fmt.Sprintf("magus workspace: this edit changes %s in %s, a declaration lease %s claims; "+leaseActorClause(fmt.Sprintf("re-partition the plan, or release %s#%s once lease %s has finished with it", rel, claim, owner.ID)),
			declaration, rel, owner.ID),
		Why: fmt.Sprintf("You claim %s in %s. Lease %s (%s) claims it as %s#%s and is %s right now, and you are lease %s. Two agents editing one declaration is the collision a claim exists to prevent; this guard is where it gets read.\n"+
			"Lease %s was last updated %s ago. If nobody holds it any more, `%s` releases its paths; magus never ends a row on its own.\n"+
			leaseActorWhy,
			strings.Join(mine, ", "), rel, owner.ID, criteriaLine(owner), rel, claim, owner.State, me.ID,
			owner.ID, time.Since(time.Unix(owner.Updated, 0)).Round(time.Second), hint.JobExit.With(owner.ID)),
	}.full()}
}

// soleClaim names the declaration owner claims in rel when that is all it holds of the
// file: no entry of its write paths covers rel whole.
func soleClaim(owner types.Job, rel string) (declaration string, ok bool) {
	var whole []string
	for _, entry := range owner.HardPaths() {
		if _, decl := types.SplitClaim(entry); decl == "" {
			whole = append(whole, entry)
		}
	}
	if _, covered, _ := declarationCovering(whole, rel); covered {
		return "", false
	}
	claims := claimsOn(owner.HardPaths(), rel)
	if len(claims) == 0 {
		return "", false
	}
	return claims[0], true
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
	i := slices.IndexFunc(claims, func(c string) bool { return types.NamesClaimedDeclaration(c, declaration) })
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
