package job

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// ungradableClaimProblems is every write or deny path of row that claims a declaration
// (`run.go#executeStages`, see types.SplitClaim) no footprint could grade, as
// WritePathClaimUngradable: an empty declaration, a claim on a glob rather than one file, a
// checkout whose version control does not place changed lines, or a file whose extension has
// no diff driver. An entry that names an existing path as written (`notes/a#b.md`) is
// ambiguous, and the problem names the two spellings that mean the path. So is a claim word
// that names more than one existing declaration of its file (`ctx`, `String`), and the
// problem names words that name one; a word that names none passes, as the declaration may
// not be written yet (see ForkNotes). A claim nothing checks reads as a boundary and is none.
// A deny path is held to the same rules because the guard grades it by the same placement,
// and one it cannot place denies the whole file while the row names one declaration.
//
// Only a row with a `#` entry reads the version control. A nil store, or one with no
// workspace root, has no tree to ask and finds nothing.
func ungradableClaimProblems(ctx context.Context, store *Store, row types.Job) types.JobProblems {
	if store == nil || store.root == "" {
		return nil
	}
	type claimedFile struct{ label, field, path, decl string }
	var (
		claimed []claimedFile
		ps      types.JobProblems
	)
	refuse := func(field, message, fix string) {
		ps = append(ps, types.JobProblem{Code: types.WritePathClaimUngradable, Field: field, Message: message, Fix: fix})
	}
	for _, list := range []struct {
		label, field string
		entries      []string
	}{{"", "write_paths", row.WritePaths}, {"deny path ", "deny_paths", row.DenyPaths}} {
		for i, entry := range list.entries {
			if !types.HasClaim(entry) {
				continue
			}
			field := fmt.Sprintf("%s[%d]", list.field, i)
			p, decl := types.SplitClaim(entry)
			literal := strings.TrimSpace(entry)
			if _, err := os.Lstat(filepath.Join(store.root, filepath.FromSlash(literal))); err == nil {
				refuse(field, list.label+fmt.Sprintf("%q is a path in this tree and also reads as a claim on %q, since an unescaped # starts a claimed declaration", entry, p),
					fmt.Sprintf("spell the path `./%s` or `%s`", literal, strings.ReplaceAll(literal, "#", `\#`)))
				continue
			}
			switch {
			case decl == "":
				refuse(field, list.label+fmt.Sprintf("%q names no declaration after its #", entry),
					fmt.Sprintf("name the declaration after the #, or claim the whole file %q", p))
			case p == "" || strings.ContainsAny(p, globMeta):
				refuse(field, list.label+fmt.Sprintf("%q claims a declaration of a pattern, and a declaration lives in one file", entry),
					fmt.Sprintf("claim the declaration in the one file that holds it, or the pattern %q whole", p))
			default:
				claimed = append(claimed, claimedFile{list.label, field, path.Clean(p), decl})
			}
		}
	}
	if len(claimed) == 0 {
		return ps
	}
	files := make([]string, len(claimed))
	for i, c := range claimed {
		files[i] = c.path
	}
	driver, drivers, err := claimDrivers(ctx, store.root, files)
	placed := map[string][]string{}
	for _, c := range claimed {
		switch {
		case err != nil:
			refuse(c.field, c.label+fmt.Sprintf("%q: %v", c.path, err), fmt.Sprintf("claim the whole file %q", c.path))
		case drivers[c.path] == "":
			refuse(c.field, c.label+fmt.Sprintf("%q has no diff driver, so no changed line of it can be placed in a declaration", c.path),
				fmt.Sprintf("give %s one in .gitattributes (`magus doctor` lists the managed ones), or claim the whole file %q", driverPattern(c.path), c.path))
		default:
			all, ok := placed[c.path]
			if !ok {
				all, _ = readDeclarations(ctx, driver, store.root, c.path)
				placed[c.path] = all
			}
			if matched := namedDeclarations(c.decl, all); len(matched) > 1 {
				message, fix := ambiguousClaimProblem(c.path, c.decl, matched, all)
				refuse(c.field, c.label+message, fix)
			}
		}
	}
	return ps
}

// maxListedClaims bounds the claims an MGS3036 refusal lists: a root magusfile can hold
// hundreds of declarations, and a dozen show the form.
const maxListedClaims = 12

// wholeFileConfigClaimProblems is every write path of row that names a project's
// configuration whole, as WritePathConfigWholeFile: a literal path, with no glob and no `#`,
// to a magusfile source or a magus.yaml the workspace loads (see projectConfig). Every job
// that changes the workspace's configuration writes that file, a target's slots or a source
// glob being one line of it, so one job holding it whole blocks every other for that line.
// The fix lists claims the file has, magusfile project keys first
// (`magusfile.buzz#project.targets.test`, `magus.yaml#jobs.stale_after`).
//
// A file whose lines this checkout cannot place, or that declares nothing yet, is left
// alone, since nothing smaller than it could be claimed. When another live job holds such a
// file, one attention request asks it to release what it does not edit (see
// requestRelease); a dry-run store files none. A nil store, or one with no workspace root,
// finds nothing.
func wholeFileConfigClaimProblems(ctx context.Context, store *Store, row types.Job) types.JobProblems {
	if store == nil || store.root == "" {
		return nil
	}
	var (
		ps    types.JobProblems
		files []string
	)
	for i, entry := range row.WritePaths {
		if types.HasClaim(entry) {
			continue
		}
		file := path.Clean(strings.TrimSpace(entry))
		if strings.ContainsAny(file, globMeta) || !projectConfig(store.root, file) {
			continue
		}
		driver, drivers, err := claimDrivers(ctx, store.root, []string{file})
		if err != nil || drivers[file] == "" {
			continue
		}
		all, ok := readDeclarations(ctx, driver, store.root, file)
		if !ok || len(all) == 0 {
			continue
		}
		ps = append(ps, types.JobProblem{
			Code:  types.WritePathConfigWholeFile,
			Field: fmt.Sprintf("write_paths[%d]", i),
			Message: fmt.Sprintf("%q holds a project's configuration whole, and every job that changes the workspace's configuration"+
				" writes that file, so a whole-file hold blocks them all for one line", entry),
			Fix: "claim the declarations and keys the job changes instead, such as " + listClaims(file, all),
		})
		files = append(files, file)
	}
	if len(ps) == 0 {
		return nil
	}
	if f, err := store.read(); err == nil {
		combined := append(slices.DeleteFunc(slices.Clone(f.Jobs), func(r types.Job) bool { return r.ID == row.ID }), row)
		for _, file := range files {
			for _, holder := range liveHolders(f.Jobs, row.ID, file) {
				// A parent holds its children's paths by design; it is not blocking them.
				if !orderedPair(combined, row.ID, holder.ID) {
					store.requestRelease(ctx, holder.ID, file, []string{file})
				}
			}
		}
	}
	return ps
}

// listClaims renders the claims on file that each name one of its declarations all, project
// keys first, as a refusal lists them.
func listClaims(file string, all []string) string {
	rank := func(d string) int {
		if strings.HasPrefix(d, "project.") {
			return 0
		}
		return 1
	}
	ordered := slices.Clone(all)
	slices.SortStableFunc(ordered, func(a, b string) int { return rank(a) - rank(b) })
	var claims []string
	for _, d := range ordered {
		word := claimWord(d, all)
		if len(namedDeclarations(word, all)) != 1 {
			continue
		}
		claims = append(claims, "`"+file+"#"+word+"`")
	}
	if len(claims) == 0 {
		return fmt.Sprintf("`%s#<declaration>`", file)
	}
	if len(claims) > maxListedClaims {
		return fmt.Sprintf("%s and %d more", strings.Join(claims[:maxListedClaims], ", "), len(claims)-maxListedClaims)
	}
	return strings.Join(claims, ", ")
}

// errVCSDisabled is claimDrivers' answer for a workspace that turned version control off.
var errVCSDisabled = errors.New("version control is disabled here, so no diff places a changed line")

// claimDrivers asks root's version control which diff driver names each path's
// declarations; the error says why it cannot, and is errVCSDisabled when version control is
// off. It returns the version control with the answer, so a caller can place a file's lines
// without resolving it again.
func claimDrivers(ctx context.Context, root string, paths []string) (types.VCSDriver, map[string]string, error) {
	res, err := vcs.Resolve(ctx, root, "", types.VCSOptions{})
	switch {
	case err != nil:
		return nil, nil, fmt.Errorf("no version control answered here: %w", err)
	case res.Source == types.VCSSourceDisabled || res.VCS == nil:
		return nil, nil, errVCSDisabled
	}
	drivers, err := res.VCS.Drivers(ctx, root, paths)
	if err != nil {
		return nil, nil, fmt.Errorf("its diff driver could not be read: %w", err)
	}
	return res.VCS, drivers, nil
}

// driverPattern is the .gitattributes pattern a driver for p would be declared under.
func driverPattern(p string) string {
	if ext := path.Ext(p); ext != "" {
		return "`*" + ext + "`"
	}
	return "`" + path.Base(p) + "`"
}

// unorderedFileShareProblems is every write path of row that would hand one claimable file
// (no glob, not a directory, not a project root, with a diff driver) to two live, unordered
// jobs at once, as WritePathFileShared: a candidate entry and a holder's entry name the same
// file, still intersect (types.PathsIntersect), and neither row is the other's ancestor,
// descendant, or depends_on partner. Each entry is reported once, against the first holder
// found. When the file is a project's configuration, it also files one attention request
// asking the holder to release it (see requestRelease).
//
// Left alone: a same-checkout or not-yet-taken holder (CheckoutRoot == ""), which
// sharedCheckoutProblems and the overlap report already watch; a holder that exited, whose
// editing is over (see [Editing]); a holder blocked on
// depends_on, which owns none of its write paths yet (types.JobBlockedOn); and a file with
// no diff driver, for which depends_on is the only move.
func unorderedFileShareProblems(ctx context.Context, store *Store, rows []types.Job, row types.Job) types.JobProblems {
	if store == nil || store.root == "" || len(row.WritePaths) == 0 {
		return nil
	}
	here, _ := filepath.Abs(store.root)
	// A re-fork replaces its stored row, so the pair is ordered by what it declares now.
	combined := append(slices.DeleteFunc(slices.Clone(rows), func(r types.Job) bool { return r.ID == row.ID }), row)
	var ps types.JobProblems
	for i, mine := range row.WritePaths {
		if p, ok := sharedFileProblem(ctx, store, rows, combined, row, here, mine); ok {
			p.Field = fmt.Sprintf("write_paths[%d]", i)
			ps = append(ps, p)
		}
	}
	return ps
}

// sharedFileProblem is unorderedFileShareProblems for the one write path mine.
func sharedFileProblem(ctx context.Context, store *Store, rows, combined []types.Job, row types.Job, here, mine string) (types.JobProblem, bool) {
	file, ok := literalClaimableFile(store.root, mine)
	if !ok {
		return types.JobProblem{}, false
	}
	if _, driven := types.MatchDiffDriver(file); !driven {
		return types.JobProblem{}, false
	}
	for _, holder := range rows {
		if holder.ID == row.ID || !Editing(holder) || len(holder.WritePaths) == 0 {
			continue
		}
		// A row nobody has taken yet is not in a different checkout from anyone.
		if holder.CheckoutRoot == "" || (here != "" && holder.CheckoutRoot == here) {
			continue
		}
		// Blocked means holder owns none of its write paths yet; the fork that
		// unblocks it runs this same check again, against whatever holds the file then.
		if _, blocked := types.JobBlockedOn(rows, holder); blocked {
			continue
		}
		if orderedPair(combined, row.ID, holder.ID) {
			continue
		}
		for _, theirs := range holder.WritePaths {
			if other, ok := literalClaimableFile(store.root, theirs); !ok || other != file {
				continue
			}
			if !types.PathsIntersect(mine, theirs) {
				continue
			}
			touched, unreadable := touchedDeclarations(ctx, store, rows, holder.ID, file)
			if projectConfig(store.root, file) {
				wanted := slices.DeleteFunc(slices.Clone(row.WritePaths), func(entry string) bool {
					other, ok := literalClaimableFile(store.root, entry)
					return !ok || other != file
				})
				store.requestRelease(ctx, holder.ID, file, wanted)
			}
			return types.JobProblem{
				Code: types.WritePathFileShared,
				Message: fmt.Sprintf("%s declares %q, and %s (%s, updated %s ago) already holds %q. Both are live and neither"+
					" is the other's parent, child, or depends_on partner, so %s has one owner unless the rows"+
					" say otherwise. %s has touched %s in it so far",
					row.ID, mine, holder.ID, holder.State, updatedAgo(holder), theirs,
					file,
					holder.ID, describeTouched(touched, unreadable)),
				Fix: splitAdvice(ctx, store, row, holder, file, touched),
			}, true
		}
	}
	return types.JobProblem{}, false
}

// literalClaimableFile is the file entry names, whole or by declaration, cleaned and
// workspace-relative, or "" when entry is a glob, the whole tree, or an existing
// directory: none of those can be shared by a claim, so unorderedFileShareProblems
// has nothing to compare them against.
func literalClaimableFile(root, entry string) (string, bool) {
	p, _ := types.SplitClaim(entry)
	p = path.Clean(strings.TrimSpace(p))
	if p == "" || p == "." || strings.ContainsAny(p, globMeta) {
		return "", false
	}
	if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(p))); err == nil && info.IsDir() {
		return "", false
	}
	return p, true
}

// orderedPair reports whether id and other are already sequenced: one an ancestor or
// descendant of the other by parent chain, walked over combined (which must carry id even
// when it is not declared yet), or one reaches the other through depends_on, any number of
// hops.
func orderedPair(combined []types.Job, id, other string) bool {
	if slices.ContainsFunc(types.JobAncestors(combined, id), func(r types.Job) bool { return r.ID == other }) {
		return true
	}
	if slices.ContainsFunc(types.JobAncestors(combined, other), func(r types.Job) bool { return r.ID == id }) {
		return true
	}
	return dependsOnTransitively(combined, id, other) || dependsOnTransitively(combined, other, id)
}

// dependsOnTransitively reports whether from reaches on by following depends_on edges over
// combined. seen guards a depends_on cycle from looping forever.
func dependsOnTransitively(combined []types.Job, from, on string) bool {
	seen := map[string]bool{from: true}
	queue := []string{from}
	for len(queue) > 0 {
		i := slices.IndexFunc(combined, func(r types.Job) bool { return r.ID == queue[0] })
		queue = queue[1:]
		if i < 0 {
			continue
		}
		for _, dep := range combined[i].DependsOn {
			if dep == on {
				return true
			}
			if !seen[dep] {
				seen[dep] = true
				queue = append(queue, dep)
			}
		}
	}
	return false
}

// touchedDeclarations are the declarations holder has changed inside file so far, sorted,
// with types.Preamble standing for lines no declaration encloses. The error says why they
// cannot be read. Reads the tree once per call; a fork the rule passes never pays for it.
func touchedDeclarations(ctx context.Context, store *Store, rows []types.Job, holder, file string) ([]string, error) {
	driver, _ := resolveDriver(ctx, store.root)
	fp := readFootprint(withFootprintCache(ctx, store.footprintCache()), driver, rows, holder)
	if !fp.known {
		return nil, errors.New(cmp.Or(fp.reason, "its footprint could not be read"))
	}
	var decls []string
	for _, r := range fp.regions {
		if loc := r.Location(); loc.Path == file {
			decls = appendUnique(decls, cmp.Or(loc.Declaration, types.Preamble))
		}
	}
	slices.Sort(decls)
	return decls, nil
}

// describeTouched is touchedDeclarations as a refusal names it.
func describeTouched(decls []string, unreadable error) string {
	if unreadable != nil {
		return fmt.Sprintf("unknown (%s)", unreadable)
	}
	if len(decls) == 0 {
		return "none yet"
	}
	named := slices.Clone(decls)
	for i, d := range named {
		if d == types.Preamble {
			named[i] = "the top of the file"
		}
	}
	return strings.Join(named, ", ")
}

// splitAdvice is what a refusal tells the new job to do about file, which holder already
// holds. When the holder's touched declarations and the job's symbol goals each name
// declarations of file, it prints the claim pair that splits the file and says whether the
// two are disjoint. magus writes neither claim: the rows are the orchestrator's. With
// nothing to compute a pair from it gives the generic advice.
func splitAdvice(ctx context.Context, store *Store, row, holder types.Job, file string, touched []string) string {
	orElse := fmt.Sprintf("add `--depends-on %s` to %s, or fold %s into %s", holder.ID, row.ID, row.ID, holder.ID)
	generic := fmt.Sprintf("Claim `%s#<declaration>` on both rows, %s", file, orElse)
	driver, _ := resolveDriver(ctx, store.root)
	if driver == nil {
		return generic
	}
	all, _ := readDeclarations(ctx, driver, store.root, file)
	var theirs, mine []string
	for _, d := range touched {
		if d != types.Preamble {
			theirs = appendUnique(theirs, claimWord(d, all))
		}
	}
	for _, g := range row.Goals {
		if g.Kind != types.GoalKindSymbol {
			continue
		}
		for _, symbol := range g.Symbols {
			if len(namedDeclarations(symbol, all)) == 1 {
				mine = appendUnique(mine, symbol)
			}
		}
	}
	claims := func(words []string, id string) string {
		quoted := make([]string, len(words))
		for i, w := range words {
			quoted[i] = "`" + file + "#" + w + "`"
		}
		return strings.Join(quoted, ", ") + " on " + id
	}
	switch {
	case len(theirs) == 0 && len(mine) == 0:
		return generic
	case len(theirs) == 0:
		return fmt.Sprintf("Claim %s (its symbol goals name it) and a declaration other than that on %s, or %s", claims(mine, row.ID), holder.ID, orElse)
	case len(mine) == 0:
		return fmt.Sprintf("Claim %s (it has touched it) and a declaration other than that on %s, or %s", claims(theirs, holder.ID), row.ID, orElse)
	}
	for _, m := range mine {
		for _, t := range theirs {
			if sameDeclaration(m, t, all) {
				return fmt.Sprintf("%s's symbol goals and %s's touched work both reach %s in it, so a claim cannot split them: %s", row.ID, holder.ID, m, orElse)
			}
		}
	}
	return fmt.Sprintf("Claim %s and %s, which name different declarations (nothing was written to either row), or %s",
		claims(theirs, holder.ID), claims(mine, row.ID), orElse)
}

// sameDeclaration reports whether the claim words a and b name one declaration of all.
func sameDeclaration(a, b string, all []string) bool {
	return a == b || slices.ContainsFunc(all, func(d string) bool { return types.NamesDeclaration(a, d) && types.NamesDeclaration(b, d) })
}

func appendUnique(list []string, s string) []string {
	if slices.Contains(list, s) {
		return list
	}
	return append(list, s)
}

// readDeclarations lists the declarations of file as its diff driver places them, in file
// order and without the preamble; false when the file cannot be read or placed. A file
// compared with nothing is all added, which is how a RegionReporter says where each of its
// declarations lies.
func readDeclarations(ctx context.Context, driver types.VCSDriver, root, file string) ([]string, bool) {
	body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
	if err != nil {
		return nil, false
	}
	regions, err := driver.RegionsBetween(ctx, root, file, nil, body)
	if err != nil {
		return nil, false
	}
	var decls []string
	for _, r := range regions {
		if d := r.Location().Declaration; d != "" && d != types.Preamble {
			decls = appendUnique(decls, d)
		}
	}
	return decls, true
}

// namedDeclarations are the entries of all that claim names.
func namedDeclarations(claim string, all []string) []string {
	return slices.DeleteFunc(slices.Clone(all), func(d string) bool { return !types.NamesDeclaration(claim, d) })
}

// ambiguousClaim is the MGS3031 reason for a claim naming several declarations of file:
// the guard and `job wait` would hold every one of them, not the one its author meant.
func ambiguousClaim(file, claim string, matched, all []string) string {
	message, fix := ambiguousClaimProblem(file, claim, matched, all)
	return message + "; " + fix
}

// ambiguousClaimProblem is ambiguousClaim as a problem's message and fix.
func ambiguousClaimProblem(file, claim string, matched, all []string) (message, fix string) {
	shown := matched[:min(len(matched), 3)]
	alternatives := make([]string, 0, len(shown))
	for _, d := range shown {
		alternatives = append(alternatives, "`"+claimWord(d, all)+"`")
	}
	message = fmt.Sprintf("%q names %d declarations of %s (such as `%s`), and a claim guards exactly one",
		file+"#"+claim, len(matched), file, strings.TrimSpace(matched[0]))
	return message, "use a word only one of them has, such as " + strings.Join(alternatives, ", ")
}

// claimWord is the shortest word of declaration that names it and no other entry of all:
// a heading's text, else an identifier (preferring one that is called, which is the name),
// else the whole line. A key path (`project.targets.test`, `jobs.stale_after`) is claimed
// whole.
func claimWord(declaration string, all []string) string {
	// Any one word of a key path also names its siblings. No declaration line a funcname
	// pattern matches is dotted without a space.
	if strings.Contains(declaration, ".") && !strings.ContainsAny(declaration, " \t#") {
		return declaration
	}
	names := func(word string) bool {
		return types.NamesDeclaration(word, declaration) && !slices.ContainsFunc(all, func(d string) bool {
			return d != declaration && types.NamesDeclaration(word, d)
		})
	}
	if strings.HasPrefix(declaration, "#") {
		if heading := strings.TrimSpace(strings.TrimLeft(declaration, "#")); names(heading) {
			return heading
		}
	}
	var first string
	for i := 0; i < len(declaration); {
		if !wordByte(declaration[i]) {
			i++
			continue
		}
		j := i
		for j < len(declaration) && wordByte(declaration[j]) {
			j++
		}
		if word := declaration[i:j]; names(word) {
			if j < len(declaration) && declaration[j] == '(' {
				return word
			}
			if first == "" {
				first = word
			}
		}
		i = j
	}
	if first != "" {
		return first
	}
	return strings.TrimSpace(declaration)
}

func wordByte(b byte) bool {
	return b == '_' || b == '$' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= 0x80
}

// ForkNotes are what a fork that passed owes its reader, one line each. A write-path claim
// that names no declaration of its file yet is allowed, since the declaration may not be
// written, and is echoed so a typo shows. A row claiming declarations of a project's
// configuration is given every claim its change there lacks (see unclaimedTouches). A
// file row shares with a job in the same checkout, or not yet taken (write_proof
// overlapping), gets the claim pair that would split it. Nothing is written to a row. A nil store, or one with no workspace root, has
// no tree to read and returns nil.
func ForkNotes(ctx context.Context, store *Store, rows []types.Job, row types.Job) []string {
	if store == nil || store.root == "" {
		return nil
	}
	var notes, configs []string
	for _, entry := range row.WritePaths {
		file, ok := literalClaimableFile(store.root, entry)
		if !ok {
			continue
		}
		_, decl := types.SplitClaim(entry)
		if decl == "" || decl == types.Preamble {
			continue
		}
		if projectConfig(store.root, file) {
			configs = appendUnique(configs, file)
		}
		driver, drivers, err := claimDrivers(ctx, store.root, []string{file})
		if err != nil || drivers[file] == "" {
			continue
		}
		all, readable := readDeclarations(ctx, driver, store.root, file)
		switch {
		case !readable:
			notes = append(notes, fmt.Sprintf("%s does not exist yet in this checkout, so %q names a declaration once it is written", file, entry))
		case len(namedDeclarations(decl, all)) == 0:
			notes = append(notes, fmt.Sprintf("%q names no declaration of %s yet; it holds one once written, so check the spelling", entry, file))
		}
	}
	for _, file := range configs {
		if note := unclaimedTouches(ctx, store, rows, row, file); note != "" {
			notes = append(notes, note)
		}
	}
	here, _ := filepath.Abs(store.root)
	type pair struct{ peer, file string }
	shared := map[pair]bool{}
	for _, peer := range rows {
		if peer.ID == row.ID || !Editing(peer) || len(peer.WritePaths) == 0 || peer.CheckoutRoot != "" && peer.CheckoutRoot != here {
			continue
		}
		for _, mine := range row.WritePaths {
			file, ok := literalClaimableFile(store.root, mine)
			if !ok || shared[pair{peer.ID, file}] {
				continue
			}
			if _, driven := types.MatchDiffDriver(file); !driven {
				continue
			}
			for _, theirs := range peer.WritePaths {
				if other, ok := literalClaimableFile(store.root, theirs); !ok || other != file || !types.PathsIntersect(mine, theirs) {
					continue
				}
				shared[pair{peer.ID, file}] = true
				var touched []string
				if peer.CheckoutRoot != "" {
					touched, _ = touchedDeclarations(ctx, store, rows, peer.ID, file)
				}
				notes = append(notes, fmt.Sprintf("%s and %s both write %s. %s", row.ID, peer.ID, file, splitAdvice(ctx, store, row, peer, file, touched)))
				break
			}
		}
	}
	return notes
}

// unclaimedTouches names every declaration of file, a project's configuration row claims
// by declaration, that row's change since its checkpoint touches and no claim of row names,
// each as the claim that adds it. One answer carries the whole key list, where a claim per
// refusal sent a worker back for each key. Empty when every touched declaration is claimed,
// and when no checkout has taken row, since nothing has changed yet.
func unclaimedTouches(ctx context.Context, store *Store, rows []types.Job, row types.Job, file string) string {
	if row.CheckoutRoot == "" {
		return ""
	}
	current := append(slices.DeleteFunc(slices.Clone(rows), func(r types.Job) bool { return r.ID == row.ID }), row)
	touched, err := touchedDeclarations(ctx, store, current, row.ID, file)
	if err != nil {
		return fmt.Sprintf("the declarations %s's change touches in %s could not be read (%v), so the claims it lacks there are not listed", row.ID, file, err)
	}
	held := claimsOnFile(row, file)
	var all []string
	if driver, _ := resolveDriver(ctx, store.root); driver != nil {
		all, _ = readDeclarations(ctx, driver, store.root, file)
	}
	var claims []string
	for _, d := range touched {
		if d == types.Preamble || slices.ContainsFunc(held, func(c string) bool { return types.NamesDeclaration(c, d) }) {
			continue
		}
		claims = appendUnique(claims, "`"+file+"#"+claimWord(d, all)+"`")
	}
	if len(claims) == 0 {
		return ""
	}
	return fmt.Sprintf("%s's change touches %d declaration(s) of %s that no claim of it names; claim them all at once: %s",
		row.ID, len(claims), file, strings.Join(claims, ", "))
}

// updatedAgo is how long ago row last wrote itself, as a refusal names it.
func updatedAgo(row types.Job) string {
	return time.Since(time.Unix(row.Updated, 0)).Round(time.Second).String()
}
