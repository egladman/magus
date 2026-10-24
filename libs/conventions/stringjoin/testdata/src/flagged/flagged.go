package flagged

type label string

func greeting(name string) string {
	return "hello, " + name + "!" // want `string built from 3 operands joined with \+: format it with fmt.Sprintf; use fmt.Sprintf`
}

func path(dir, name string) string {
	return dir + "/" + name // want `path built from 3 operands joined with \+: join it with filepath.Join`
}

func script(cmd string) string {
	return "set -e\n" + cmd + "\nexit 0" // want `multi-line text built from 3 operands joined with \+: write it as a raw string or a text/template`
}

func nested(a, b, c, d string) string {
	return a + b + c + d // want `string built from 4 operands`
}

func inCall(a, b, c string) int {
	return len(a + b + c) // want `string built from 3 operands`
}

func typed(l label, s string) label {
	return l + label(s) + "." // want `string built from 3 operands`
}

const repo = "https://example.com/repo"

func link(path string) string {
	return repo + "/" + path // want `string built from 3 operands joined with \+: format it with fmt.Sprintf`
}

func multilineWins(dir string) string {
	return dir + "/\n" + dir // want `multi-line text built from 3 operands`
}
