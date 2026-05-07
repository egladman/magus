package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/egladman/magus/internal/cache"
)

const signingKeyEnv = "MAGUS_CACHE_SIGNING_KEY"

func configCacheKey(ctx context.Context, root string, args []string) error {
	fs := flag.NewFlagSet("config cache key", flag.ContinueOnError)
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
		fmt.Fprintf(os.Stderr, "magus config cache key: unknown subcommand %q\n\n", sub)
		fs.Usage()
		return fmt.Errorf("magus config cache key: unknown subcommand %q", sub)
	}
}

// configCacheKeyGenerate mints a keypair and prints it to stdout once. The secret
// seed is never written to disk — a signing key must not come to rest on a
// developer machine; copy it straight into a CI secret store.
func configCacheKeyGenerate(args []string) error {
	fs := flag.NewFlagSet("config cache key generate", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus config cache key generate")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Mint a new Ed25519 keypair for remote-cache signing. Prints the secret")
		fmt.Fprintln(os.Stderr, "seed, the public key, the derived keyid, and a ready-to-paste")
		fmt.Fprintln(os.Stderr, "trusted_keys snippet for magus.yaml. The secret is shown ONCE, never to disk.")
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	km, err := cache.GenerateSigningKey()
	if err != nil {
		return fmt.Errorf("magus config cache key generate: %w", err)
	}

	fmt.Printf("keyid: %s\n\n", km.KeyID)
	fmt.Println("┌─ SECRET — the signing key. Anyone holding it can publish trusted cache")
	fmt.Println("│  artifacts. Store it as the MAGUS_CACHE_SIGNING_KEY secret in your CI for")
	fmt.Println("│  trusted pushes ONLY. Do NOT commit it, save it to disk, or paste it")
	fmt.Println("│  anywhere else. It is shown once.")
	fmt.Printf("└─ %s=%s\n\n", signingKeyEnv, km.SeedB64)
	fmt.Println("Public key — not secret. Add it to cache.remote.trusted_keys in magus.yaml:")
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
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus config cache key id [<base64-public-key>]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Show the derived keyid and pubkey for a public key. With no")
		fmt.Fprintln(os.Stderr, "argument, reads MAGUS_CACHE_SIGNING_KEY (a seed) and derives its public")
		fmt.Fprintln(os.Stderr, "identity — useful to confirm which key CI signs with. The seed is never")
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
