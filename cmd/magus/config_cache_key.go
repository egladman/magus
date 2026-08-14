package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/egladman/magus/internal/cache"
)

const signingKeyEnv = "MAGUS_CACHE_SIGNING_KEY"

func configCacheKey(_ context.Context, _ string, args []string) error {
	fs := flag.NewFlagSet("config cache key", flag.ContinueOnError)
	bindDisplayFlags(fs)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus config cache key <subcommand>")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Manage Ed25519 keys for remote-cache signing.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Subcommands:")
		fmt.Fprintln(os.Stderr, "  generate   mint a new keypair (alias: gen)")
		fmt.Fprintln(os.Stderr, "  id         show the keyid + pubkey for a key (for cache.remote.trusted_keys)")
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		fs.Usage()
		return nil
	}
	sub, subArgs := rest[0], rest[1:]
	switch sub {
	case "generate", "gen":
		return configCacheKeyGenerate(subArgs)
	case "id":
		return configCacheKeyID(subArgs)
	case "-h", "--help", "help":
		fs.Usage()
		return nil
	default:
		fs.Usage()
		return usagef("magus config cache key: unknown subcommand %q", sub)
	}
}

// signingKeyOutput is the generated keypair as a record, so `-o template` can hand
// the seed to a secret store over a pipe. The JSON TAGS are the template surface -
// `-o template` projects the -o json names, so it is `{{.seed}}`, not `{{.Seed}}` -
// and renaming one breaks a documented command line. Treat the tags as API.
type signingKeyOutput struct {
	KeyID  string `json:"keyid" yaml:"keyid"`
	Seed   string `json:"seed" yaml:"seed"`
	PubKey string `json:"pubkey" yaml:"pubkey"`
}

// configCacheKeyGenerate mints a keypair and prints it once. The secret seed is
// never written to disk — a signing key must not come to rest on a developer
// machine; copy it straight into a CI secret store.
//
// The default output is for a human who will paste the seed into a password
// manager, so it stays prose. `-o template='{{.seed}}'` is for the other custody
// model: pipe the seed into a secret store and never see it at all. In that mode
// the warnings go to stderr, because a pipe reader wants exactly the secret and
// nothing else on stdout.
func configCacheKeyGenerate(args []string) error {
	fs := flag.NewFlagSet("config cache key generate", flag.ContinueOnError)
	bindDisplayFlags(fs)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus config cache key generate [-o template='{{.seed}}']")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Mint a new Ed25519 keypair for remote-cache signing. Prints the secret")
		fmt.Fprintln(os.Stderr, "seed, the public key, the derived keyid, and a ready-to-paste")
		fmt.Fprintln(os.Stderr, "trusted_keys snippet for magus.yaml. The secret is shown ONCE, never to disk.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "With -o json|yaml|template the record is {keyid, seed, pubkey} and every")
		fmt.Fprintln(os.Stderr, "warning moves to stderr, so stdout carries only what you asked for:")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "  magus config cache key generate -o template='{{.seed}}' \\")
		fmt.Fprintln(os.Stderr, "    | gh secret set MAGUS_CACHE_SIGNING_KEY")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "--tee is refused here: it writes structured output to a FILE, and a signing")
		fmt.Fprintln(os.Stderr, "key must not come to rest on disk.")
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	// --tee mirrors structured output into a file. Every other command wants that;
	// this one must never have it, or `-o json --tee keys.json` quietly does the one
	// thing the whole key-handling design forbids. Refused rather than ignored: a
	// silently-dropped --tee would leave you believing you had a copy.
	if global.tee != "" {
		return usagef("magus config cache key generate: --tee writes to a file and a signing key must not come to rest on disk; drop it, or pipe -o template='{{.seed}}' straight into your secret store")
	}

	km, err := cache.GenerateSigningKey()
	if err != nil {
		return fmt.Errorf("magus config cache key generate: %w", err)
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	if opts.Format != outputText {
		// Warnings to stderr so a pipe gets only the requested field. Printed BEFORE
		// the value: on a terminal the caller should read them, and on a pipe they
		// are out of the way either way.
		fmt.Fprintf(os.Stderr, "keyid: %s\n", km.KeyID)
		fmt.Fprintf(os.Stderr, "public key (not secret; add to cache.remote.trusted_keys in magus.yaml):\n  %s\n", km.PubB64)
		fmt.Fprintln(os.Stderr, "the seed is on stdout and is shown ONCE; it is not written to disk.")
		return emitFormatted(opts, signingKeyOutput{KeyID: km.KeyID, Seed: km.SeedB64, PubKey: km.PubB64})
	}

	fmt.Printf("keyid: %s\n\n", km.KeyID)
	fmt.Println("┌─ SECRET - the signing key. Anyone holding it can publish trusted cache")
	fmt.Println("│  artifacts. Store it as the MAGUS_CACHE_SIGNING_KEY secret in your CI for")
	fmt.Println("│  trusted pushes ONLY. Do NOT commit it, save it to disk, or paste it")
	fmt.Println("│  anywhere else. It is shown once.")
	fmt.Printf("└─ %s=%s\n\n", signingKeyEnv, km.SeedB64)
	fmt.Println("Public key - not secret. Add it to cache.remote.trusted_keys in magus.yaml:")
	fmt.Println("    cache:")
	fmt.Println("      remote:")
	fmt.Println("        trusted_keys:")
	fmt.Printf("          - \"%s\"\n\n", km.PubB64)
	fmt.Println("Gold-standard custody: run this inside a one-shot CI job and write the seed")
	fmt.Println("straight to your secret store, so it never touches a developer machine.")
	return nil
}

// configCacheKeyID prints the keyid and pubkey for a key: from a public
// key argument, or from MAGUS_CACHE_SIGNING_KEY (a seed) when none is given —
// never echoing the seed.
func configCacheKeyID(args []string) error {
	fs := flag.NewFlagSet("config cache key id", flag.ContinueOnError)
	bindDisplayFlags(fs)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus config cache key id [<base64-public-key>]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Show the derived keyid and pubkey for a public key. With no")
		fmt.Fprintln(os.Stderr, "argument, reads MAGUS_CACHE_SIGNING_KEY (a seed) and derives its public")
		fmt.Fprintln(os.Stderr, "identity - useful to confirm which key CI signs with. The seed is never")
		fmt.Fprintln(os.Stderr, "printed.")
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	var info cache.KeyInfo
	var err error
	switch rest := fs.Args(); {
	case len(rest) >= 1:
		info, err = cache.TrustedKeyInfo(rest[0])
	default:
		seed := os.Getenv(signingKeyEnv)
		if seed == "" {
			fs.Usage()
			return fmt.Errorf("magus config cache key id: provide a public key argument or set %s", signingKeyEnv)
		}
		info, err = cache.SigningKeyInfo(seed)
	}
	if err != nil {
		return fmt.Errorf("magus config cache key id: %w", err)
	}

	fmt.Printf("keyid:   %s\n", info.KeyID)
	fmt.Printf("pubkey:  %s\n", info.PubB64)
	fmt.Println("(add the pubkey under cache.remote.trusted_keys in magus.yaml)")
	return nil
}
