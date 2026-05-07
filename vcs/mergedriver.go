package vcs

import "strings"

// replaceManagedSection replaces the begin…end marker section with newSection, or appends it.
func replaceManagedSection(text, newSection, begin, end string) string {
	startIdx := strings.Index(text, begin)
	endIdx := strings.Index(text, end)

	if startIdx >= 0 && endIdx >= 0 && endIdx > startIdx {
		endOfEnd := strings.Index(text[endIdx:], "\n")
		var tail string
		if endOfEnd >= 0 {
			tail = text[endIdx+endOfEnd+1:]
		}
		prefix := strings.TrimRight(text[:startIdx], "\n")
		if prefix != "" {
			prefix += "\n"
		}
		tail = strings.TrimLeft(tail, "\n")
		if tail != "" {
			tail = "\n" + tail
		}
		return prefix + newSection + tail
	}

	trimmed := strings.TrimRight(text, "\n")
	if trimmed != "" {
		return trimmed + "\n\n" + newSection
	}
	return newSection
}
