package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/egladman/magus/internal/registry"
)

// selfRefreshCmd implements `magus self refresh`: fetch, verify, and cache every
// configured registry source.
//
// NOT `update` or `upgrade`. apt's update-refreshes-metadata / upgrade-installs
// pair is correct and is a footgun - the distinction is invisible to anyone it has
// not already bitten - and `magus self update` already means apt's *upgrade*, so
// adopting the pair would mean renaming a shipped verb into the more confusing
// half. `sync` was the other candidate and fails on UNIX grounds: it means
// flush-to-disk, and daemon.maintenance.sync_graph already uses it here for a local
// operation. `refresh` cannot be misread as "replace my binary", which is the only
// confusion that costs anyone anything.
//
// It carries no build tag. Only the UPDATER is what -tags noselfupdate removes; a
// distro-packaged magus, where the package manager owns the binary, still reads
// data files. Stripping this with the updater would leave those installs reporting
// `never synced` forever while the hint named a subcommand their build did not
// have - the same 404-shaped dead end by a different road.
func selfRefreshCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("self refresh", flag.ContinueOnError)
	bindDisplayFlags(fs)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus self refresh")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Fetch, verify, and cache the registry each source in")
		fmt.Fprintln(os.Stderr, "$XDG_CONFIG_HOME/magus/registry.d/ declares.")
		fmt.Fprintln(os.Stderr, "This downloads a DATA FILE. It does not upgrade magus.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Set MAGUS_OFFLINE=1 to make any outbound attempt a named error.")
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	sources, err := registry.LoadSources()
	if err != nil {
		return err
	}
	if len(sources) == 0 {
		dir, dirErr := registry.SourcesDir()
		if dirErr != nil {
			return dirErr
		}
		// Not an error. Nothing configured is a legitimate resting state, and a
		// nonzero exit here would make "I do not want this" look like a fault.
		fmt.Printf("no registry sources configured; add one to %s\n", dir)
		return nil
	}

	var failed int
	for _, src := range sources {
		got, err := registry.Refresh(ctx, src, nil)
		if err != nil {
			failed++
			fmt.Fprintf(os.Stderr, "[error] %v\n", err)
			continue
		}
		fmt.Printf("%s: %d product(s), generated %s (%s)\n",
			src.Name, len(got.Registry.EOL), got.Registry.GeneratedAt.UTC().Format(time.RFC3339), got.State)
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d source(s) failed to refresh", failed, len(sources))
	}
	return nil
}

// selfRegistryCmd implements `magus self registry`: what is cached, how old, from
// where, and whether it verified. Reads the local cache and sends nothing.
func selfRegistryCmd(_ context.Context, args []string) error {
	fs := flag.NewFlagSet("self registry", flag.ContinueOnError)
	bindDisplayFlags(fs)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus self registry")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Report each configured registry source: its state, the age of its")
		fmt.Fprintln(os.Stderr, "data, and where it came from. Reads the local cache; sends nothing.")
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	cached, err := registry.Load()
	if err != nil {
		return err
	}
	if len(cached) == 0 {
		dir, dirErr := registry.SourcesDir()
		if dirErr != nil {
			return dirErr
		}
		fmt.Printf("no registry sources configured; add one to %s\n", dir)
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SOURCE\tSTATE\tDATA AGE\tPRODUCTS\tURL")
	for _, c := range cached {
		age, products := "-", "-"
		if c.Registry != nil {
			age = roughAge(c.Age)
			products = fmt.Sprintf("%d", len(c.Registry.EOL))
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", c.Source.Name, c.State, age, products, c.Source.URL)
	}
	return w.Flush()
}

// roughAge renders a duration the way a human would say it. A registry's age is
// measured in days once it is interesting at all.
func roughAge(d time.Duration) string {
	switch days := int(d.Hours() / 24); {
	case days >= 1:
		return fmt.Sprintf("%dd", days)
	case d >= time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return "under an hour"
	}
}
