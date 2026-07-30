package types

// Cacheable is implemented by each kind of work a target can dispatch to. There are
// exactly two - a spell's op and a magusfile's exported target - and the cache needs
// the same thing from both: the identity of what will actually run.
//
// Stating that as one interface is the point. Before it, the hasher wrote the target
// NAME into the key as a stand-in for the work, which is wrong in both directions: two
// names over identical work could never share an entry, and one name over two different
// bodies (a magusfile target shadowing a spell op) hashed as if they were the same
// thing. A contributor answers for itself instead of the key guessing from a label.
type Cacheable interface {
	// Cache returns how this unit of work participates in the cache for target.
	Cache(target string) CacheContribution
}

// CacheContribution is one unit of work's account of itself to the cache. It is a type
// rather than a bare slice so the participation can grow - what a run reads, what it
// writes - without every implementer changing shape each time.
type CacheContribution struct {
	key []string
}

// NewCacheContribution builds a contribution from the lines identifying the work, in a
// stable order. Order is preserved rather than sorted: these lines describe a command,
// where argument order is meaning, and reordering them would move the key without the
// work changing.
func NewCacheContribution(key ...string) CacheContribution {
	return CacheContribution{key: key}
}

// Key returns the lines that identify this work, written into the cache key verbatim.
// Nil means the work adds nothing to the key, which is not the same as adding an empty
// line: a contributor with nothing to say must not change the hash.
func (c CacheContribution) Key() []string { return c.key }

// MagusfileSpellName is the spell a project's own magusfile is bound as. A target it
// exports shadows a spell op of the same name: the magusfile is the workspace's own
// override, so it decides what "go-build" means there.
const MagusfileSpellName = "magusfile"

// magusfileTarget is a magusfile's exported target, seen as a unit of work.
//
// A magusfile body is Buzz, not a command, so there is nothing to serialize the way a
// spell op's argv serializes. Its text already reaches the key through the magusfile's
// own Sources entry, so what remains to distinguish `build` from `go-build` in one file
// is the entry point - the target name. Naming that here, rather than leaving the hasher
// to write a bare target line, is what keeps the two contributors symmetric: both say
// what they run, one just has less to say.
type magusfileTarget struct{}

// MagusfileTarget returns the Cacheable for a magusfile's exported targets.
func MagusfileTarget() Cacheable { return magusfileTarget{} }

// Cache implements Cacheable.
func (magusfileTarget) Cache(target string) CacheContribution {
	if target == "" {
		return CacheContribution{}
	}
	return NewCacheContribution("magusfile-target:" + target)
}
