package main

import (
	"regexp"
	"strings"
)

// secrets are the shapes redacted before judging: what looks like an email,
// a token, a key or a private key block. A git hash is left alone, since a
// commit named in a sentence is evidence a rule reads.
var secrets = []*regexp.Regexp{
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`),
	regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,}`),
	regexp.MustCompile(`\b(gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,})\b`),
	regexp.MustCompile(`\b(AKIA|ASIA)[0-9A-Z]{16}\b`),
	regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}`),
	regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`),
	regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`),
}

// assignedSecret is a long value after a word that names a secret; the word
// stays, so the sentence still reads.
var assignedSecret = regexp.MustCompile(`(?i)\b((?:bearer|token|api[_-]?key|secret|password)(?:\s*[:=]\s*|\s+)["']?)[A-Za-z0-9._~+/-]{16,}=*`)

// mixedSecret is a long run of letters and digits in both cases with several
// digits, the shape of a generated key; a hex hash, lowercase only, does not
// qualify.
var mixedSecret = regexp.MustCompile(`\b[A-Za-z0-9_-]{32,}\b`)

const redacted = "[REDACTED]"

func redact(text string) string {
	for _, re := range secrets {
		text = re.ReplaceAllString(text, redacted)
	}

	text = assignedSecret.ReplaceAllString(text, "${1}"+redacted)

	return mixedSecret.ReplaceAllStringFunc(text, func(m string) string {
		digits := 0

		for _, r := range m {
			if r >= '0' && r <= '9' {
				digits++
			}
		}

		if digits >= 3 && strings.ContainsAny(m, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") && strings.ContainsAny(m, "abcdefghijklmnopqrstuvwxyz") {
			return redacted
		}

		return m
	})
}
