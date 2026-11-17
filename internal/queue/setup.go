package queue

import (
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"

	"mvdan.cc/sh/v3/syntax"

	"github.com/egladman/magus/internal/queue/types"
)

// WriteSetup renders what describe read as a script a person reads and then runs
// step by step: every fact is a comment, every step a command or a link to open.
func WriteSetup(w io.Writer, provider, base string, caps types.Capabilities) error {
	var b strings.Builder
	methods := make([]string, len(caps.Methods))
	for i, m := range caps.Methods {
		methods[i] = string(m)
	}
	fmt.Fprintf(&b, "# %s on %s: merge methods %s; stacks merge %s", provider, base, strings.Join(methods, ", "), caps.StackMerge)
	if caps.QueueLabel != "" {
		fmt.Fprintf(&b, "; a stack queues by the label %q", caps.QueueLabel+"<method>")
	}
	b.WriteString("\n")
	if caps.RequiredApprovals == 0 {
		fmt.Fprintf(&b, "# %s requires no approval, so the queue merges a change nobody approved; it adds no review rule of its own\n", base)
	} else {
		fmt.Fprintf(&b, "# %s requires %d approving reviews at the commit a review of a change's head covers\n", base, caps.RequiredApprovals)
	}
	s := caps.Setup
	if s == nil {
		fmt.Fprintf(&b, "# no setup: --status-context is empty, or provider %s reports none\n", provider)
		_, err := io.WriteString(w, b.String())
		return err
	}
	fmt.Fprintf(&b, "# the queue posts %q as %s\n", s.StatusContext, s.Credential)
	if len(s.RequiredChecks) == 0 {
		fmt.Fprintf(&b, "# %s requires no check\n", base)
	}
	for _, rc := range s.RequiredChecks {
		from := "any source"
		if rc.Integration != "" {
			from = "integration " + rc.Integration
		}
		seen := "not seen reported"
		if len(rc.Events) > 0 {
			seen = "seen on " + strings.Join(rc.Events, ", ")
		}
		fmt.Fprintf(&b, "# %s requires %q from %s (%s)\n", base, rc.Context, from, seen)
	}
	for _, st := range s.Settings {
		fmt.Fprintf(&b, "# %s is %s; the queue needs %s\n", st.Name, st.Value, st.Want)
	}
	if a := s.App; a != nil {
		fmt.Fprintf(&b, "# app %s is integration %s", a.Slug, a.ID)
		if a.ClientID != "" {
			fmt.Fprintf(&b, ", client id %s", a.ClientID)
		}
		b.WriteString("\n")
	}
	if len(s.Steps) == 0 {
		b.WriteString("# nothing left to set up\n")
	}
	for i, st := range s.Steps {
		fmt.Fprintf(&b, "\n# %d. %s\n", i+1, st.Title)
		if st.URL != "" {
			fmt.Fprintf(&b, "# open %s\n", st.URL)
			continue
		}
		b.WriteString(strings.TrimRight(st.Command, "\n") + "\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// RenderRerun is the describe command line fs was parsed from, quoted for a shell, with
// app, the provider's own value, in place of the --app it was given. names are the flags
// it carries over when fs was given them.
func RenderRerun(root string, fs *flag.FlagSet, names []string, app string) string {
	given := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { given[f.Name] = true })
	words := []string{"magus"}
	if root != "" {
		words = append(words, "--root", ShellWord(root))
	}
	words = append(words, "queue", "describe")
	for _, name := range names {
		if given[name] {
			words = append(words, "--"+name, ShellWord(fs.Lookup(name).Value.String()))
		}
	}
	return strings.Join(append(words, "--app", ShellWord(app)), " ")
}

// ShellWord quotes s as one word for bash, or Go-quotes it when it holds a byte no shell
// quote can carry.
func ShellWord(s string) string {
	q, err := syntax.Quote(s, syntax.LangBash)
	if err != nil {
		return strconv.Quote(s)
	}
	return q
}
