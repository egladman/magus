package mergequeue

// model is an in-memory version control system the step tests and the property harness
// drive the queue against, with modelProvider as its provider. A tree is a map of path to content and a
// three-way merge is per file: a path both sides changed differently conflicts. That is
// coarser than git's line merge, which only means more conflicts; everything the queue
// decides from a merge (which base, which tree, which paths) behaves as it does in git.
//
// One store serves every checkout and both the queue's clones: FetchRef reads the
// remote's refs, and a commit exists once anything made it.

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"sync"
)

type tree = map[string]string

type mcommit struct {
	tree    string
	parents []string
	author  Person
	subject string
	seq     int // creation order, which RangeCommits sorts newest first by
}

type mcheckout struct {
	head      string
	merging   string          // the revision a merge in progress brings in
	recorded  tree            // what a commit without paths records
	work      tree            // the working tree
	incoming  tree            // the merged revision's side of each conflict
	conflicts map[string]bool // path -> one side or both deleted it
}

// generatedFile lists, at a revision, the paths it marks generated: one per line, a
// trailing "/" marking a directory.
const generatedFile = ".generated"

type model struct {
	mu        sync.Mutex
	commits   map[string]*mcommit
	trees     map[string]tree
	refs      map[string]string // the remote's
	checkouts map[string]*mcheckout
	seq       int
	failFetch map[string]bool // refs FetchRef cannot read
}

var _ VCS = (*model)(nil)

func newModel() *model {
	return &model{commits: map[string]*mcommit{}, trees: map[string]tree{}, refs: map[string]string{},
		checkouts: map[string]*mcheckout{}, failFetch: map[string]bool{}}
}

func hash(parts ...string) string {
	h := sha1.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// putTree stores t and returns its id. Callers hold mu.
func (m *model) putTree(t tree) string {
	keys := slices.Sorted(maps.Keys(t))
	parts := make([]string, 0, 2*len(keys))
	for _, k := range keys {
		parts = append(parts, k, t[k])
	}
	id := "t" + hash(parts...)[1:]
	if _, ok := m.trees[id]; !ok {
		m.trees[id] = maps.Clone(t)
	}
	return id
}

// putCommit stores a commit and returns its id; the same inputs give the same id.
// Callers hold mu.
func (m *model) putCommit(t string, parents []string, author Person, subject string) string {
	id := hash(append([]string{t, author.Name, author.Email, subject}, parents...)...)
	if _, ok := m.commits[id]; !ok {
		m.seq++
		m.commits[id] = &mcommit{tree: t, parents: slices.Clone(parents), author: author, subject: subject, seq: m.seq}
	}
	return id
}

// commit records files (a nil-valued entry deletes) on parent, the way an author would,
// and returns the new commit. An empty parent starts a history.
func (m *model) commit(parent string, subject string, files map[string]*string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := tree{}
	var parents []string
	if parent != "" {
		t = maps.Clone(m.trees[m.commits[parent].tree])
		parents = []string{parent}
	}
	for p, body := range files {
		if body == nil {
			delete(t, p)
		} else {
			t[p] = *body
		}
	}
	return m.putCommit(m.putTree(t), parents, Person{Name: "author", Email: "author@example.invalid"}, subject)
}

// mergeCommit records a merge of theirs into ours with tree t, the way an author's merge
// or GitHub's "Update branch" would.
func (m *model) mergeCommit(ours, theirs string, t tree, subject string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.putCommit(m.putTree(t), []string{ours, theirs}, Person{Name: "author", Email: "author@example.invalid"}, subject)
}

func (m *model) set(ref, id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refs[ref] = id
}

func (m *model) ref(ref string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.refs[ref]
}

// files returns rev's tree, rev being a commit or a tree id; nil when it is neither.
func (m *model) files(rev string) tree {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, _ := m.treeOf(rev)
	return maps.Clone(t)
}

func str(s string) *string { return &s }

// treeOf resolves a commit or a tree id. Callers hold mu.
func (m *model) treeOf(rev string) (tree, error) {
	if t, ok := m.trees[rev]; ok {
		return t, nil
	}
	c, ok := m.commits[rev]
	if !ok {
		return nil, fmt.Errorf("unknown revision %q", rev)
	}
	return m.trees[c.tree], nil
}

// ancestors is rev and everything beneath it. Callers hold mu.
func (m *model) ancestors(rev string) map[string]bool {
	out := map[string]bool{}
	stack := []string{rev}
	for len(stack) > 0 {
		r := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if out[r] {
			continue
		}
		out[r] = true
		if c, ok := m.commits[r]; ok {
			stack = append(stack, c.parents...)
		}
	}
	return out
}

// mergeBase is the newest common ancestor no other common ancestor descends from, or ""
// for unrelated histories. Callers hold mu.
func (m *model) mergeBase(a, b string) string {
	ia, ib := m.ancestors(a), m.ancestors(b)
	var common []string
	for r := range ia {
		if ib[r] {
			common = append(common, r)
		}
	}
	best := ""
	for _, c := range common {
		dominated := false
		for _, o := range common {
			if o != c && m.ancestors(o)[c] {
				dominated = true
				break
			}
		}
		if !dominated && (best == "" || m.commits[c].seq > m.commits[best].seq) {
			best = c
		}
	}
	return best
}

// merge3 merges per file; a conflicted path carries both sides in markers, or the
// surviving side when one side deleted it.
func merge3(b, o, t tree) (tree, []string, map[string]bool) {
	out := tree{}
	deleted := map[string]bool{}
	var conflicts []string
	paths := map[string]bool{}
	for _, x := range []tree{b, o, t} {
		for p := range x {
			paths[p] = true
		}
	}
	for _, p := range slices.Sorted(maps.Keys(paths)) {
		bv, bok := b[p]
		ov, ook := o[p]
		tv, tok := t[p]
		same := func(v1 string, ok1 bool, v2 string, ok2 bool) bool { return ok1 == ok2 && v1 == v2 }
		switch {
		case same(ov, ook, tv, tok), same(tv, tok, bv, bok):
			if ook {
				out[p] = ov
			}
		case same(ov, ook, bv, bok):
			if tok {
				out[p] = tv
			}
		default:
			conflicts = append(conflicts, p)
			switch {
			case ook && tok:
				out[p] = "<<<<<<<\n" + ov + "=======\n" + tv + ">>>>>>>\n"
			case ook:
				out[p], deleted[p] = ov, true
			case tok:
				out[p], deleted[p] = tv, true
			default:
				deleted[p] = true
			}
		}
	}
	return out, conflicts, deleted
}

func (m *model) FetchRef(_ context.Context, ref string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.refs[ref]
	if !ok || m.failFetch[ref] {
		return "", fmt.Errorf("couldn't find remote ref %s", ref)
	}
	return id, nil
}

func (m *model) FetchCommit(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.commits[id]; !ok {
		return fmt.Errorf("no commit %s on the remote", id)
	}
	return nil
}

func (m *model) IsAncestor(_ context.Context, ancestor, descendant string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.commits[ancestor]; !ok {
		return false, fmt.Errorf("unknown revision %q", ancestor)
	}
	return m.ancestors(descendant)[ancestor], nil
}

func (m *model) RangeFiles(_ context.Context, base, head string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	from, err := m.treeOf(m.mergeBase(base, head))
	if err != nil {
		from = tree{}
	}
	to, err := m.treeOf(head)
	if err != nil {
		return nil, err
	}
	return diff(from, to), nil
}

func diff(a, b tree) []string {
	var out []string
	for p := range a {
		if bv, ok := b[p]; !ok || bv != a[p] {
			out = append(out, p)
		}
	}
	for p := range b {
		if _, ok := a[p]; !ok {
			out = append(out, p)
		}
	}
	slices.Sort(out)
	return out
}

func (m *model) RangeCommits(_ context.Context, base, head string, paths []string) ([]Commit, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.commits[head]; !ok {
		return nil, fmt.Errorf("unknown revision %q", head)
	}
	skip := m.ancestors(base)
	var out []Commit
	for id := range m.ancestors(head) {
		if skip[id] {
			continue
		}
		c := m.commits[id]
		if len(paths) > 0 {
			before := tree{}
			if len(c.parents) > 0 {
				before = m.trees[m.commits[c.parents[0]].tree]
			}
			if !slices.ContainsFunc(diff(before, m.trees[c.tree]), func(p string) bool { return slices.Contains(paths, p) }) {
				continue
			}
		}
		out = append(out, m.describe(id))
	}
	slices.SortFunc(out, func(a, b Commit) int { return m.commits[b.ID].seq - m.commits[a.ID].seq })
	return out, nil
}

// describe is rev as FindCommit reports it. Callers hold mu.
func (m *model) describe(id string) Commit {
	c := m.commits[id]
	return Commit{ID: id, Parents: slices.Clone(c.parents), Author: c.author, Subject: c.subject}
}

func (m *model) FindCommit(_ context.Context, rev string) (Commit, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.commits[rev]; !ok {
		return Commit{}, fmt.Errorf("unknown revision %q", rev)
	}
	return m.describe(rev), nil
}

func (m *model) TreeID(_ context.Context, rev string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.trees[rev]; ok {
		return rev, nil
	}
	c, ok := m.commits[rev]
	if !ok {
		return "", fmt.Errorf("unknown revision %q", rev)
	}
	return c.tree, nil
}

func (m *model) DiffTrees(_ context.Context, a, b string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ta, err := m.treeOf(a)
	if err != nil {
		return nil, err
	}
	tb, err := m.treeOf(b)
	if err != nil {
		return nil, err
	}
	return diff(ta, tb), nil
}

func (m *model) MergeTrees(_ context.Context, tm TreeMerge) (TreeMergeResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b := tm.Base
	if b == "" {
		b = m.mergeBase(tm.Ours, tm.Theirs)
	}
	bt := tree{}
	if b != "" {
		var err error
		if bt, err = m.treeOf(b); err != nil {
			return TreeMergeResult{}, err
		}
	}
	ot, err := m.treeOf(tm.Ours)
	if err != nil {
		return TreeMergeResult{}, err
	}
	tt, err := m.treeOf(tm.Theirs)
	if err != nil {
		return TreeMergeResult{}, err
	}
	out, conflicts, _ := merge3(bt, ot, tt)
	return TreeMergeResult{Tree: m.putTree(out), Conflicts: conflicts}, nil
}

func (m *model) GeneratedPaths(_ context.Context, rev string, paths []string) (map[string]bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, err := m.treeOf(rev)
	if err != nil {
		return nil, err
	}
	marks := strings.Fields(t[generatedFile])
	out := map[string]bool{}
	for _, p := range paths {
		for _, mk := range marks {
			if p == mk || strings.HasSuffix(mk, "/") && strings.HasPrefix(p, mk) {
				out[p] = true
			}
		}
	}
	return out, nil
}

func (m *model) checkout(dir string) (*mcheckout, error) {
	co, ok := m.checkouts[dir]
	if !ok {
		return nil, fmt.Errorf("no checkout at %s", dir)
	}
	return co, nil
}

func (m *model) CreateCheckout(_ context.Context, dir, rev string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.checkouts[dir]; ok {
		return fmt.Errorf("%s already exists", dir)
	}
	t, err := m.treeOf(rev)
	if err != nil {
		return err
	}
	m.checkouts[dir] = &mcheckout{head: rev, recorded: maps.Clone(t), work: maps.Clone(t)}
	return nil
}

func (m *model) RemoveCheckout(_ context.Context, dir string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.checkouts[dir]; !ok {
		return fmt.Errorf("no checkout at %s", dir)
	}
	delete(m.checkouts, dir)
	return nil
}

func (m *model) StartMerge(_ context.Context, dir, rev string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	co, err := m.checkout(dir)
	if err != nil {
		return err
	}
	if co.merging != "" {
		return errors.New("a merge is already in progress")
	}
	bt := tree{}
	if b := m.mergeBase(co.head, rev); b != "" {
		bt, _ = m.treeOf(b)
	}
	ot, _ := m.treeOf(co.head)
	tt, err := m.treeOf(rev)
	if err != nil {
		return err
	}
	out, conflicts, deleted := merge3(bt, ot, tt)
	co.merging, co.work, co.incoming = rev, out, maps.Clone(tt)
	co.recorded = maps.Clone(out)
	co.conflicts = map[string]bool{}
	for _, p := range conflicts {
		co.conflicts[p] = deleted[p]
	}
	return nil
}

func (m *model) AbortMerge(_ context.Context, dir string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	co, err := m.checkout(dir)
	if err != nil {
		return err
	}
	t, _ := m.treeOf(co.head)
	co.merging, co.conflicts, co.work, co.recorded = "", nil, maps.Clone(t), maps.Clone(t)
	return nil
}

func (m *model) Conflicts(_ context.Context, dir string) ([]ConflictedPath, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	co, err := m.checkout(dir)
	if err != nil {
		return nil, err
	}
	var out []ConflictedPath
	for _, p := range slices.Sorted(maps.Keys(co.conflicts)) {
		out = append(out, ConflictedPath{Path: p, Deleted: co.conflicts[p]})
	}
	return out, nil
}

func (m *model) KeepIncoming(_ context.Context, dir string, paths []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	co, err := m.checkout(dir)
	if err != nil {
		return err
	}
	for _, p := range paths {
		if v, ok := co.incoming[p]; ok {
			co.work[p] = v
		}
	}
	return nil
}

func (m *model) MarkResolved(_ context.Context, dir string, paths []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	co, err := m.checkout(dir)
	if err != nil {
		return err
	}
	for _, p := range paths {
		record(co, p)
		delete(co.conflicts, p)
	}
	return nil
}

func record(co *mcheckout, p string) {
	if v, ok := co.work[p]; ok {
		co.recorded[p] = v
	} else {
		delete(co.recorded, p)
	}
}

func (m *model) RemoveConflicts(_ context.Context, dir string, paths []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	co, err := m.checkout(dir)
	if err != nil {
		return err
	}
	for _, p := range paths {
		delete(co.work, p)
		delete(co.recorded, p)
		delete(co.conflicts, p)
	}
	return nil
}

func (m *model) DirtyFiles(_ context.Context, dir string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	co, err := m.checkout(dir)
	if err != nil {
		return nil, err
	}
	t, _ := m.treeOf(co.head)
	return diff(t, co.work), nil
}

func (m *model) Commit(_ context.Context, dir string, c CheckoutCommit) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c.Author.Name == "" || c.Committer.Name == "" {
		return "", errors.New("a commit needs an author and a committer")
	}
	co, err := m.checkout(dir)
	if err != nil {
		return "", err
	}
	if len(co.conflicts) > 0 {
		return "", fmt.Errorf("unresolved conflicts in %s", strings.Join(slices.Sorted(maps.Keys(co.conflicts)), ", "))
	}
	for _, p := range c.Paths {
		record(co, p)
	}
	headTree, _ := m.treeOf(co.head)
	parents := []string{co.head}
	if co.merging != "" {
		parents = append(parents, co.merging)
	} else if maps.Equal(co.recorded, headTree) {
		return "", errors.New("nothing to commit")
	}
	id := m.putCommit(m.putTree(co.recorded), parents, c.Author, c.Message)
	co.head, co.merging, co.incoming = id, "", nil
	return id, nil
}

func (m *model) CommitTree(_ context.Context, c TreeCommit) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c.Author.Name == "" || c.Committer.Name == "" {
		return "", errors.New("a commit needs an author and a committer")
	}
	t, err := m.treeOf(c.Tree)
	if err != nil {
		return "", err
	}
	for _, p := range c.Parents {
		if _, ok := m.commits[p]; !ok {
			return "", fmt.Errorf("unknown parent %q", p)
		}
	}
	return m.putCommit(m.putTree(t), c.Parents, c.Author, c.Message), nil
}

func (m *model) Push(_ context.Context, p PushLease) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if cur, ok := m.refs[p.Ref]; !ok || cur != p.Expected {
		return ErrStaleLease
	}
	m.refs[p.Ref] = p.To
	return nil
}

func (m *model) Bundle(_ context.Context, file string, r BundleRange) error {
	return os.WriteFile(file, []byte(r.Head), 0o644)
}

func (m *model) Unbundle(ctx context.Context, file string) error {
	b, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	return m.FetchCommit(ctx, string(b))
}

// write sets a file in a checkout's working tree, the way a regeneration hook does.
func (m *model) write(dir, path, body string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.checkouts[dir].work[path] = body
}

// read reads a file from a checkout's working tree.
func (m *model) read(dir, path string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.checkouts[dir].work[path]
	return v, ok
}
