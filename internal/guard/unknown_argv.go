package guard

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/commands"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/libs/cmdline"
)

// closedGroupUsage matches a usage line that enumerates its subcommands.
// Such a group takes no free positional. `ls` and `query` do, and the tree
// does not describe them.
var closedGroupUsage = regexp.MustCompile(`<[^<>]*\|[^<>]*>`)

var verbosityFlag = regexp.MustCompile(`^v+$`)

// unknownArgv is a magus call the command tree does not accept.
type unknownArgv struct {
	typed   string
	nearest string
	// flag is set when the words resolved and a flag did not.
	flag bool
}

// unknownMagusArgv refuses a magus call the judging binary's command tree
// rejects. It never allows: a call the tree accepts keeps the rules about
// the shape around it.
func unknownMagusArgv(deps Dependencies, work []hint.Invocation) (ShellVerdict, bool) {
	for _, c := range work {
		if !isMagusInvocation(c) {
			continue
		}
		bad, ok := unknownArgvOf(c.Args)
		if !ok {
			continue
		}
		bin := hint.BinaryName()
		deny := fmt.Sprintf("%s (%s) has no command or flag `%s`",
			cmp.Or(deps.Binary, "this magus"), cmp.Or(deps.BinaryVersion, "an unknown version"), bad.typed)
		if bad.nearest != "" {
			deny += fmt.Sprintf("; the nearest is `%s %s`", bin, bad.nearest)
		}
		why := fmt.Sprintf("`%s help` lists the commands this build has.", bin)
		if bad.flag {
			why = fmt.Sprintf("magus exits 2 on a flag the command does not bind. `%s %s -h` lists the ones it does.", bin, bad.nearest)
		}
		argv := strings.Join(append([]string{c.Name}, c.Args...), " ")
		return ShellVerdict{
			Deny: deny + ".",
			Why:  why,
			Rule: denyRule{Name: denyRuleUnknownMagusArgv, Arg: argv},
		}, true
	}
	return ShellVerdict{}, false
}

// unknownArgvOf resolves args against commands.Root. Words past `--` belong
// to the tool a target runs, so they are not read.
func unknownArgvOf(args []string) (unknownArgv, bool) {
	words := magusSubcommandWords(args)
	if len(words) == 0 || words[0] == commands.Help.Name {
		return unknownArgv{}, false
	}
	level := commands.Root.Children
	var path []string
	var chain []cmdline.Command
	for _, w := range words {
		i := slices.IndexFunc(level, func(c cmdline.Command) bool { return c.Name == w })
		if i < 0 {
			if len(chain) > 0 && !closedGroupUsage.MatchString(chain[len(chain)-1].Usage) {
				break
			}
			names := make([]string, len(level))
			for j, c := range level {
				names[j] = c.Name
			}
			near := hint.Nearest(w, names)
			if near != "" {
				near = strings.Join(append(slices.Clone(path), near), " ")
			}
			return unknownArgv{typed: strings.Join(append(slices.Clone(path), w), " "), nearest: near}, true
		}
		chain = append(chain, level[i])
		path = append(path, w)
		level = level[i].Children
		if len(level) == 0 {
			break
		}
	}
	name, ok := unboundFlag(args, chain)
	if !ok {
		return unknownArgv{}, false
	}
	return unknownArgv{typed: "--" + name, nearest: strings.Join(path, " "), flag: true}, true
}

// unboundFlag returns the first flag in args that no command on the path,
// no global flag and no help flag binds.
func unboundFlag(args []string, chain []cmdline.Command) (string, bool) {
	bound := map[string]cmdline.Flag{}
	for _, c := range slices.Concat([]cmdline.Command{commands.Root}, chain) {
		for _, f := range c.Flags {
			bound[f.Name] = f
		}
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			return "", false
		}
		if len(a) < 2 || a[0] != '-' {
			continue
		}
		name, _, joined := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(a, "-"), "-"), "=")
		takes, global := MagusFlagTakesValue(name)
		f, own := bound[name]
		if own {
			takes = f.Kind != cmdline.Bool && f.Kind != cmdline.Custom
		}
		if !own && !global {
			base := strings.TrimPrefix(name, "no-")
			bf, negates := bound[base]
			_, globalBase := MagusFlagTakesValue(base)
			switch {
			case name == "h" || name == "help" || verbosityFlag.MatchString(name):
			case negates && base != name && bf.Kind == cmdline.Bool:
			case globalBase && base != name:
			default:
				return name, true
			}
		}
		if takes && !joined {
			i++
		}
	}
	return "", false
}
