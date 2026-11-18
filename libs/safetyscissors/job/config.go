package job

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/egladman/magus/libs/apiversion"
)

// Config is what [Open] opens a Store from. Dir is required; every seam is set by an
// [Option], and each one left out has the default its option names.
type Config struct {
	// Dir holds jobs.jsonl, attempts.jsonl, grants.jsonl, grades.jsonl and the lock.
	Dir string

	storeConfig
	errs []error
}

// storeConfig is everything a Store carries but its mutex and its dry run, so a view
// copies it whole and sets only what differs.
type storeConfig struct {
	path string
	// err is why the store could not be opened, which every operation reports: a Store
	// that cannot name its file must not read as an empty ledger, since absent rows are
	// how a guard decides nothing is leased.
	err      error
	root     string
	pinned   *Actor
	actor    func() Actor
	origin   func(context.Context) Origin
	denials  Denials
	hook     WriteHook
	retain   func(context.Context, *Rows, time.Time) error
	sweep    func(context.Context, Rows) (Rows, error)
	notices  func(Notice)
	clock    func() time.Time
	declared func(ctx context.Context, path, decl string, body []byte) string
	head     func(context.Context) string
	version  apiversion.APIVersion
	migrate  Migrate
	creds    wireEnum[CredentialKind]
	wrapErr  func(error) error
	tree     func(context.Context) (string, error)
	verifier Verifier
	command  CheckCommand
	checks   *checkSpec
}

// Option sets one of a Store's seams on a [Config] before [Open] reads it. An option
// given a nil or empty value records an *[OptionError] on the Config rather than being
// ignored, so Open refuses the store.
type Option func(*Config)

// OptionError is an [Option] given a value it cannot use.
type OptionError struct {
	Option  string
	Problem string
}

func (e *OptionError) Error() string {
	return fmt.Sprintf("job: option %s: %s", e.Option, e.Problem)
}

// ConfigError is a [Config] field [Open] cannot open a store from.
type ConfigError struct {
	Field   string
	Problem string
}

func (e *ConfigError) Error() string {
	return fmt.Sprintf("job: config %s: %s", e.Field, e.Problem)
}

func (c *Config) fail(option, problem string) {
	c.errs = append(c.errs, &OptionError{Option: option, Problem: problem})
}

// set records fn in dst, or refuses option when fn is nil. The caller says which, since
// a value of a type parameter cannot be compared to nil.
func set[F any](c *Config, option string, fn F, isNil bool, dst *F) {
	if isNil {
		c.fail(option, "nil function")
		return
	}
	*dst = fn
}

// WithRoot names the checkout a row's paths are relative to, for the digest a release
// records. Without one every release records [DigestAbsent] and a [Notice] says so. An
// empty root is an *[OptionError].
func WithRoot(root string) Option {
	return func(c *Config) {
		if root == "" {
			c.fail("WithRoot", "empty root")
			return
		}
		c.root = root
	}
}

// WithActor resolves who each write is made by, at every write; an unbound writer returns
// the zero Actor. [Store.As] pins one instead. A store with neither refuses every row
// write and grant with an [*UnconfiguredError] rather than guess who wrote it. A nil
// function is an *[OptionError].
func WithActor(actor func() Actor) Option {
	return func(c *Config) { set(c, "WithActor", actor, actor == nil, &c.actor) }
}

// WithOrigin stamps where a write came from onto what the store records of it: a row's
// registered_by, an entry's and a grant's by. Without it those are the zero Origin. A nil
// function is an *[OptionError].
func WithOrigin(origin func(context.Context) Origin) Option {
	return func(c *Config) { set(c, "WithOrigin", origin, origin == nil, &c.origin) }
}

// WithDenials reads the denials a fork of an ended job folds into its last attempt.
// Without it they are unknown. A nil function is an *[OptionError].
func WithDenials(d Denials) Option {
	return func(c *Config) { set(c, "WithDenials", d, d == nil, &c.denials) }
}

// WithWriteHook runs hook at each [WriteStage] of a row write, inside the write, while
// the store's locks are held: it must not touch the store. A nil hook is an
// *[OptionError].
func WithWriteHook(hook WriteHook) Option {
	return func(c *Config) { set(c, "WithWriteHook", hook, hook == nil, &c.hook) }
}

// WithRetention runs retain over the rows of every rewrite, inside the lock and before
// the write, with the store's clock: it may drop rows that are over and stamp
// [Rows.Retained]. Without it every row is kept. A nil function is an *[OptionError].
func WithRetention(retain func(ctx context.Context, rows *Rows, now time.Time) error) Option {
	return func(c *Config) { set(c, "WithRetention", retain, retain == nil, &c.retain) }
}

// WithSweep runs sweep over the rows [Store.List] reads before it returns them, for a
// caller that ends rows it can prove dead. The sweep may write through the store. Without
// it List returns the rows as stored. A nil function is an *[OptionError].
func WithSweep(sweep func(ctx context.Context, rows Rows) (Rows, error)) Option {
	return func(c *Config) { set(c, "WithSweep", sweep, sweep == nil, &c.sweep) }
}

// WithNotices sends each [Notice] to notice. A store without it refuses every row write
// and grant with an [*UnconfiguredError], since a write is where a store notices. A nil
// function is an *[OptionError].
func WithNotices(notice func(Notice)) Option {
	return func(c *Config) { set(c, "WithNotices", notice, notice == nil, &c.notices) }
}

// WithClock is the store's clock for what [Store.Now] reads. Without it, time.Now. A nil
// clock is an *[OptionError].
func WithClock(clock func() time.Time) Option {
	return func(c *Config) { set(c, "WithClock", clock, clock == nil, &c.clock) }
}

// WithDeclarationDigest digests one declaration of the file at path, whose content is
// body, for the release of a claim. It answers a sha256 digest of the lines the
// declaration spans, or one of the Digest markers. Without it a released declaration
// records [DigestUnreadable] and a [Notice] says so. A nil function is an *[OptionError].
func WithDeclarationDigest(digest func(ctx context.Context, path, decl string, body []byte) string) Option {
	return func(c *Config) { set(c, "WithDeclarationDigest", digest, digest == nil, &c.declared) }
}

// WithHead resolves the committed revision an unbound fork records as a row's
// Authority. Without it such a fork records none. A nil function is an *[OptionError].
func WithHead(head func(context.Context) string) Option {
	return func(c *Config) { set(c, "WithHead", head, head == nil, &c.head) }
}

// WithMigrations is the store version this build writes, as its header's api_version, and
// how a file at another is converted to it, the way a Kubernetes conversion takes an
// object between versions of its group. Without it the store writes [APIVersion] and
// refuses a file at any other. A version that does not parse or a nil migrate is an
// *[OptionError].
func WithMigrations(version apiversion.APIVersion, migrate Migrate) Option {
	return func(c *Config) {
		if _, err := apiversion.Parse(string(version)); err != nil {
			c.fail("WithMigrations", err.Error())
			return
		}
		if migrate == nil {
			c.fail("WithMigrations", "nil function")
			return
		}
		c.version, c.migrate = version, migrate
	}
}

// WithCredentialKinds are the credential kinds an origin the store records may carry,
// stored as the caller spells them. A stored kind none of them names is refused rather
// than read. Without it an origin carrying any kind is refused. No kinds, or an empty
// one, is an *[OptionError].
func WithCredentialKinds(kinds ...CredentialKind) Option {
	return func(c *Config) {
		if len(kinds) == 0 {
			c.fail("WithCredentialKinds", "no kinds")
			return
		}
		for _, k := range kinds {
			if strings.TrimSpace(string(k)) == "" {
				c.fail("WithCredentialKinds", "an empty kind")
				return
			}
		}
		c.creds = newWireEnum(kinds...)
	}
}

// WithErrorWrap passes each refusal the store raises of its own, a typed error, through
// wrap, for a caller that adds its own codes or remedy. A nil function is an
// *[OptionError].
func WithErrorWrap(wrap func(error) error) Option {
	return func(c *Config) { set(c, "WithErrorWrap", wrap, wrap == nil, &c.wrapErr) }
}

// WithTree digests the tree a check reads, the way the caller defines it: a revision plus
// a digest of the uncommitted changes, a hash of a module, anything stable. The store
// compares digests and never interprets one. Without it [Store.Witness], [Store.Attest],
// [Store.ReportRun] and [Store.Grade] refuse with a [*GradingUnconfiguredError], since no
// grade could tell a run of this tree from a run of an older one. A nil function is an
// *[OptionError].
func WithTree(tree func(context.Context) (string, error)) Option {
	return func(c *Config) { set(c, "WithTree", tree, tree == nil, &c.tree) }
}

// WithVerifier lets [Store.Attest] accept the statements v vouches for. Without it Attest
// refuses every statement rather than record one unchecked. A nil v is an *[OptionError].
func WithVerifier(v Verifier) Option {
	return func(c *Config) { set(c, "WithVerifier", v, v == nil, &c.verifier) }
}

// WithCheckCommand names the command a check postcondition is run by. The store never reads what
// a check names (see [WithCheckSpec]), so it derives no argv itself. Without it
// [Store.Witness] refuses. A nil command is an *[OptionError].
func WithCheckCommand(command CheckCommand) Option {
	return func(c *Config) { set(c, "WithCheckCommand", command, command == nil, &c.command) }
}

// Open returns the ledger kept in cfg.Dir. The files are created by the first write.
// cfg.Dir is the caller's: Open resolves nothing about a repository.
//
// It fails with every problem found, joined: an *[OptionError] for each refused option
// and a *[ConfigError] for an empty Dir. It returns ctx.Err() as is once ctx is done.
func Open(ctx context.Context, cfg Config, opts ...Option) (*Store, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	errs := cfg.errs
	if strings.TrimSpace(cfg.Dir) == "" {
		errs = append(errs, &ConfigError{Field: "Dir", Problem: "empty: a store opens on the directory that holds it"})
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	sc := cfg.storeConfig
	sc.path = filepath.Join(cfg.Dir, jobsFileName)
	if sc.version == "" {
		sc.version = APIVersion
	}
	if sc.creds.known == nil {
		sc.creds = newWireEnum[CredentialKind]()
	}
	return &Store{storeConfig: sc}, nil
}
