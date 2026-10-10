package prose

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// MessageRunes is the length cap [JudgeMessage] holds a message to when its
// caller names none: about two lines of a terminal, room for a verdict, one
// command and a ref.
const MessageRunes = 160

// JudgeMessage runs the [KindMessage] rules over text, holding it to maxRunes
// runes, or to [MessageRunes] when maxRunes is 0. Text is judged as written:
// a caller standing in for a value the text does not hold, such as a format
// verb, replaces it with one rune first. Each finding's line is its line in
// text.
func JudgeMessage(text string, maxRunes int) []Finding {
	if maxRunes <= 0 {
		maxRunes = MessageRunes
	}

	text = strings.ReplaceAll(text, "\r\n", "\n")

	return run(input{kind: KindMessage, text: text, lines: strings.Split(text, "\n"), maxRunes: maxRunes})
}

// lineAt is the 1-based line of text holding offset.
func lineAt(text string, offset int) int {
	return 1 + strings.Count(text[:offset], "\n")
}

func messageLength(in input) []Finding {
	n := utf8.RuneCountInString(strings.TrimSpace(in.text))
	if n <= in.maxRunes {
		return nil
	}

	return []Finding{{
		Message: fmt.Sprintf("Keep a message to %d runes (this one has %d): state the verdict and one next "+
			"command, and move the rationale behind a ref.", in.maxRunes, n),
		Line: 1,
	}}
}

// causalJoin matches a join that adds a reason to a clause. A message gives
// one reason at most; a second is rationale the ref carries.
var causalJoin = regexp.MustCompile(`(?i)(?:\s(?:so|because|since|therefore|hence)\s|;\s|,\s+(?:which|meaning)\s|\swhich means\s)`)

func messageRationale(in input) []Finding {
	masked := mentionsMasked(in.text)

	joins := causalJoin.FindAllStringIndex(masked, -1)
	if len(joins) < 2 {
		return nil
	}

	at := joins[1]

	return []Finding{{
		Message: fmt.Sprintf("Give a message one reason (this one joins %d): keep the verdict and move the "+
			"rest behind a ref.", len(joins)),
		Match: strings.TrimSpace(in.text[at[0]:at[1]]),
		Line:  lineAt(in.text, at[0]),
	}}
}

// codeSpan matches a backticked span. One holding a space is a command; a
// lone word in backticks is a name or a path.
var codeSpan = regexp.MustCompile("`[^`\n]+`")

func messageCommands(in input) []Finding {
	var commands [][]int

	for _, at := range codeSpan.FindAllStringIndex(in.text, -1) {
		if strings.Contains(strings.TrimSpace(in.text[at[0]+1:at[1]-1]), " ") {
			commands = append(commands, at)
		}
	}

	if len(commands) < 2 {
		return nil
	}

	at := commands[1]

	return []Finding{{
		Message: fmt.Sprintf("Name one next command (this message names %d): keep the one to run first.",
			len(commands)),
		Match: in.text[at[0]:at[1]],
		Line:  lineAt(in.text, at[0]),
	}}
}

var (
	// componentTag is a lowercase word opening a message as its speaker's
	// label, such as "magus: " or "server: ".
	componentTag = regexp.MustCompile(`^\s*[a-z][a-z-]*: `)
	// bracketMarker is an uppercase label in brackets, such as "[AGENT]".
	bracketMarker = regexp.MustCompile(`\[[A-Z][A-Z0-9_-]*\]`)
)

func messageTag(in input) []Finding {
	var out []Finding

	if at := componentTag.FindStringIndex(in.text); at != nil {
		m := strings.TrimSpace(in.text[at[0]:at[1]])
		out = append(out, Finding{
			Message: fmt.Sprintf("Drop the leading '%s' tag: open with the verdict.", m),
			Match:   m,
			Line:    lineAt(in.text, at[0]),
		})
	}

	masked := blankBackticks(in.text)
	for _, at := range bracketMarker.FindAllStringIndex(masked, -1) {
		m := in.text[at[0]:at[1]]
		out = append(out, Finding{
			Message: fmt.Sprintf("Drop the '%s' marker: a message is plain text for whoever reads it.", m),
			Match:   m,
			Line:    lineAt(in.text, at[0]),
		})
	}

	return out
}
