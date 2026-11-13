package guard

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/egladman/magus/internal/commands"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/libs/clikit/command"
)

// waitForm is a command the wait rules may serve as the way to wait.
type waitForm struct {
	words []string // the path below the binary
	flag  string   // the flag that makes a call wait; empty when none does
}

var (
	// job exec runs workspace code, so the tree cannot call it Wait.
	noticeForm = waitForm{words: pathBelowBinary(hint.JobExec), flag: "timeout"}

	// budget_wait is a config key, not a flag on these leaves, so the
	// tree cannot call them Wait either.
	budgetRunForm      = waitForm{words: pathBelowBinary(hint.Run)}
	budgetAffectedForm = waitForm{words: pathBelowBinary(hint.Affected)}
)

func pathBelowBinary(c hint.Command) []string { return c.Argv()[1:] }

func (f waitForm) String() string {
	return fmt.Sprintf("%s %s", hint.BinaryName(), strings.Join(f.words, " "))
}

func (f waitForm) serves(words, flags []string) bool {
	if len(words) < len(f.words) || !slices.Equal(words[:len(f.words)], f.words) {
		return false
	}
	return f.flag == "" || statusFlag(flags, f.flag)
}

// waitRemedies is every command the wait rules serve: the tree's Wait
// leaves and flags, then the forms the tree cannot declare.
var waitRemedies = sync.OnceValue(func() []waitForm {
	var forms []waitForm
	command.Walk(commands.Root, func(path string, c command.Command) {
		if len(c.Children) > 0 {
			return
		}
		words := strings.Fields(path)
		if c.Effect == command.EffectWait {
			forms = append(forms, waitForm{words: words})
		}
		for _, f := range c.Flags {
			if f.Effect == command.EffectWait {
				forms = append(forms, waitForm{words: words, flag: f.Name})
			}
		}
	})
	return append(forms, noticeForm, budgetRunForm, budgetAffectedForm)
})

// servesWait reports whether argv, binary first, is a waitRemedies form.
// Words past `--` belong to the command a notice runs and are not read.
func servesWait(argv []string) bool {
	if len(argv) < 2 || filepath.Base(argv[0]) != "magus" {
		return false
	}
	flags := argv[1:]
	if i := slices.Index(flags, "--"); i >= 0 {
		flags = flags[:i]
	}
	words := magusSubcommandWords(flags)
	return slices.ContainsFunc(waitRemedies(), func(f waitForm) bool { return f.serves(words, flags) })
}

// boundedRunArgv is run backgrounded under the notice form, stopped at
// bound. bin is the binary as the reader spelled it.
func boundedRunArgv(bin, bound string, run []string) []string {
	argv := hint.JobExec.Argv("--timeout", bound, "--")
	argv[0] = bin
	return append(argv, run...)
}
