package config

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
	"mvdan.cc/sh/v3/syntax"

	"github.com/egladman/magus/internal/ward"
)

// ReleaseCommand is the shell line that installs a release for a workspace declaring
// constraint as its required_version, "" for none. It names ward.PinnedRelease, so the
// guard and MGS1021 name the same release: a range bounded from above may refuse the
// newest, and self update does not read magus.yaml.
func ReleaseCommand(constraint string) string {
	argv := []string{"magus", "self", "update"}
	if pin := ward.PinnedRelease(constraint); pin != "" {
		argv = append(argv, "--version", pin)
	}
	return shellLine(argv)
}

// SourceCommand is the shell line that rebuilds a binary built from checkout's own
// sources and older than them: link pkg into output, or, when bootstrap is set, run
// ward.BootstrapCommand in checkout, the module's own build for a checkout of magus.
func SourceCommand(checkout, pkg, output string, bootstrap bool) string {
	if bootstrap {
		return shellLine(bootstrapIn(checkout))
	}
	return shellLine([]string{"go", "-C", checkout, "build", "-o", output, "./" + pkg})
}

// RequiredVersionAt is the required_version root's magus.yaml declares, read leniently: a
// binary too old to decode the rest of the file still reads its floor. "" for none.
func RequiredVersionAt(root string) string {
	for _, name := range []string{Filename, DottedFilename} {
		if data, err := os.ReadFile(filepath.Join(root, name)); err == nil {
			return requiredVersion(data)
		}
	}
	return ""
}

// CheckoutHookAt is the checkout.bootstrap root's magus.yaml declares,
// read leniently like [RequiredVersionAt].
// "" for none, or for a path that leaves root.
func CheckoutHookAt(root string) string {
	for _, name := range []string{Filename, DottedFilename} {
		if data, err := os.ReadFile(filepath.Join(root, name)); err == nil {
			return checkoutHook(data)
		}
	}
	return ""
}

func checkoutHook(data []byte) string {
	var head struct {
		Checkout struct {
			Bootstrap string `yaml:"bootstrap"`
		} `yaml:"checkout"`
	}
	if yaml.Unmarshal(data, &head) != nil {
		return ""
	}
	hook := head.Checkout.Bootstrap
	if hook == "" || !filepath.IsLocal(filepath.FromSlash(hook)) {
		return ""
	}
	return path.Clean(filepath.ToSlash(hook))
}

// ReadyCheckoutCommand is the line that runs hook, a workspace's
// checkout.bootstrap, on the checkout the shell is in. The bootstrap builds
// ./magus alone; this installs the dependencies and harness tool rows.
// "" when hook is "".
func ReadyCheckoutCommand(hook string) string {
	if hook == "" {
		return ""
	}
	return shellLine([]string{"./magus", "buzz", hook, "--", "--here", "--verify-ready"})
}

func requiredVersion(data []byte) string {
	var head struct {
		RequiredVersion string `yaml:"required_version"`
	}
	if yaml.Unmarshal(data, &head) != nil {
		return ""
	}
	return head.RequiredVersion
}

// bootstrapIn is ward.BootstrapCommand with -C naming dir, so it builds that checkout from
// whatever directory the host runs it in.
func bootstrapIn(dir string) []string {
	words := strings.Fields(ward.BootstrapCommand)
	at := slices.Index(words, "go")
	if at < 0 {
		return words
	}
	return slices.Concat(words[:at+1], []string{"-C", dir}, words[at+1:])
}

// shellLine joins argv into one line a POSIX shell splits back into argv. A leading
// NAME=value word that needs no quoting stays an assignment.
func shellLine(argv []string) string {
	quoted := make([]string, len(argv))
	for i, a := range argv {
		name, value, assigns := strings.Cut(a, "=")
		if i == 0 && assigns && syntax.ValidName(name) {
			quoted[i] = fmt.Sprintf("%s=%s", name, shellWord(value))
			continue
		}
		quoted[i] = shellWord(a)
	}
	return strings.Join(quoted, " ")
}

func shellWord(s string) string {
	q, err := syntax.Quote(s, syntax.LangPOSIX)
	if err != nil {
		// Only a NUL byte fails, and no path, flag or version holds one.
		return strconv.Quote(s)
	}
	return q
}
