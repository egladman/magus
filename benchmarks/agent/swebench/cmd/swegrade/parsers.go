package main

import (
	"regexp"
	"strings"
)

// The parsers below are ports of swebench/harness/log_parsers/python.py from
// github.com/SWE-bench/SWE-bench, which is distributed under the MIT License:
//
//	Copyright (c) 2023 Carlos E. Jimenez, John Yang, Alexander Wettig, Shunyu Yao,
//	Kexin Pei, Ofir Press, Karthik Narasimhan
//
//	Permission is hereby granted, free of charge, to any person obtaining a copy
//	of this software and associated documentation files (the "Software"), to deal
//	in the Software without restriction, including without limitation the rights
//	to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
//	copies of the Software, and to permit persons to whom the Software is
//	furnished to do so, subject to the following conditions:
//
//	The above copyright notice and this permission notice shall be included in
//	all copies or substantial portions of the Software.
//
//	THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
//	IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
//	FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
//	AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
//	LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
//	OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
//	THE SOFTWARE.
//
// Each port keeps the upstream quirks on purpose: a verdict that differs from the
// reference harness on the same log is a grading bug, not a cleanup. Only the
// parsers SWE-bench Verified names in its log_parser column are here.

const (
	statusFailed  = "FAILED"
	statusPassed  = "PASSED"
	statusSkipped = "SKIPPED"
	statusError   = "ERROR"
	statusXfail   = "XFAIL"
)

var statuses = []string{statusFailed, statusPassed, statusSkipped, statusError, statusXfail}

// statusMap is a test name to status mapping as one parser reads it off a log.
type statusMap map[string]string

// parsers keys the log_parser column of a dataset row. The aliases mirror the
// upstream assignments (parse_log_astropy = parse_log_pytest_v2, and so on).
var parsers = map[string]func(string) statusMap{
	"parse_log_pytest":     parsePytest,
	"parse_log_flask":      parsePytest,
	"parse_log_xarray":     parsePytest,
	"parse_log_requests":   parsePytestOptions,
	"parse_log_pylint":     parsePytestOptions,
	"parse_log_astropy":    parsePytestV2,
	"parse_log_scikit":     parsePytestV2,
	"parse_log_sphinx":     parsePytestV2,
	"parse_log_django":     parseDjango,
	"parse_log_sympy":      parseSympy,
	"parse_log_matplotlib": parseMatplotlib,
	"parse_log_seaborn":    parseSeaborn,
}

var skipSummaryCount = regexp.MustCompile(`^\[\d+\]$`)

// isSkipSummary is true for pytest's "SKIPPED [N] path:line: reason" summary line,
// where [N] is a count and not a test. Scoped to SKIPPED, as upstream is: two
// Verified PASS_TO_PASS lists literally expect a "[100%]" entry from a wrapped
// PASSED line, so dropping every bare bracketed count would fail them.
func isSkipSummary(status, name string) bool {
	return status == statusSkipped && skipSummaryCount.MatchString(name)
}

func startsWithStatus(line string) bool {
	for _, s := range statuses {
		if strings.HasPrefix(line, s) {
			return true
		}
	}
	return false
}

func endsWithStatus(line string) bool {
	for _, s := range statuses {
		if strings.HasSuffix(line, s) {
			return true
		}
	}
	return false
}

func parsePytest(log string) statusMap {
	m := statusMap{}
	for _, line := range strings.Split(log, "\n") {
		if !startsWithStatus(line) {
			continue
		}
		if strings.HasPrefix(line, statusFailed) {
			line = strings.ReplaceAll(line, " - ", " ")
		}
		f := strings.Fields(line)
		if len(f) <= 1 || isSkipSummary(f[0], f[1]) {
			continue
		}
		m[f[1]] = f[0]
	}
	return m
}

var optionPattern = regexp.MustCompile(`(.*?)\[(.*)\]`)

func parsePytestOptions(log string) statusMap {
	m := statusMap{}
	for _, line := range strings.Split(log, "\n") {
		if !startsWithStatus(line) {
			continue
		}
		if strings.HasPrefix(line, statusFailed) {
			line = strings.ReplaceAll(line, " - ", " ")
		}
		f := strings.Fields(line)
		if len(f) <= 1 || isSkipSummary(f[0], f[1]) {
			continue
		}
		name := f[1]
		if g := optionPattern.FindStringSubmatch(name); g != nil {
			main, option := g[1], g[2]
			if strings.HasPrefix(option, "/") && !strings.HasPrefix(option, "//") && !strings.Contains(option, "*") {
				parts := strings.Split(option, "/")
				option = "/" + parts[len(parts)-1]
			}
			name = main + "[" + option + "]"
		}
		m[name] = f[0]
	}
	return m
}

var (
	ansiColor = regexp.MustCompile(`\[(\d+)m`)
	// A django test's "ok" pushed onto its own line by one of three long
	// multiline prints; upstream matches these over the whole log.
	djangoInterrupted = []*regexp.Regexp{
		regexp.MustCompile(`(?m)^(.*?)\s\.\.\.\sTesting against Django installed in ((?s:.*?)) silenced\)\.\nok$`),
		regexp.MustCompile(`(?m)^(.*?)\s\.\.\.\sInternal Server Error: /(.*)/\nok$`),
		regexp.MustCompile(`(?m)^(.*?)\s\.\.\.\sSystem check identified no issues \(0 silenced\)\nok$`),
	}
	sympyFailureHeader = regexp.MustCompile(`(_*) (.*)\.py:(.*) (_*)`)
)

// stripControl drops bytes 1 through 31, which is what upstream's str.maketrans
// table removes once the color codes are gone.
func stripControl(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 1 && r <= 31 {
			return -1
		}
		return r
	}, s)
}

func parsePytestV2(log string) statusMap {
	m := statusMap{}
	for _, line := range strings.Split(log, "\n") {
		line = stripControl(ansiColor.ReplaceAllString(line, ""))
		switch {
		case startsWithStatus(line):
			if strings.HasPrefix(line, statusFailed) {
				line, _, _ = strings.Cut(line, " - ")
			}
			f := strings.Fields(line)
			if len(f) >= 2 && !isSkipSummary(f[0], f[1]) {
				m[strings.Join(f[1:], " ")] = f[0]
			}
		case endsWithStatus(line):
			f := strings.Fields(line)
			if len(f) >= 2 {
				m[strings.Join(f[:len(f)-1], " ")] = f[len(f)-1]
			}
		}
	}
	return m
}

func parseDjango(log string) statusMap {
	m := statusMap{}
	var prevTest string
	havePrev := false
	for _, line := range strings.Split(log, "\n") {
		line = strings.TrimSpace(line)

		if strings.Contains(line, "--version is equivalent to version") {
			m["--version is equivalent to version"] = statusPassed
		}
		if strings.Contains(line, " ... ") {
			prevTest = strings.SplitN(line, " ... ", 2)[0]
			havePrev = true
		}

		for _, suffix := range []string{" ... ok", " ... OK", " ...  OK"} {
			if !strings.HasSuffix(line, suffix) {
				continue
			}
			// Upstream's one-instance patch for django__django-7188, whose result
			// prints on the same line as a migration notice.
			if strings.HasPrefix(line, "Applying sites.0002_alter_domain_unique...test_no_migrations") {
				_, after, _ := strings.Cut(line, "...")
				line = strings.TrimSpace(after)
			}
			m[line[:strings.LastIndex(line, suffix)]] = statusPassed
			break
		}
		if strings.Contains(line, " ... skipped") {
			m[strings.SplitN(line, " ... skipped", 2)[0]] = statusSkipped
		}
		if strings.HasSuffix(line, " ... FAIL") {
			m[strings.SplitN(line, " ... FAIL", 2)[0]] = statusFailed
		}
		if strings.HasPrefix(line, "FAIL:") {
			m[strings.Fields(line)[1]] = statusFailed
		}
		if strings.HasSuffix(line, " ... ERROR") {
			m[strings.SplitN(line, " ... ERROR", 2)[0]] = statusError
		}
		if strings.HasPrefix(line, "ERROR:") {
			m[strings.Fields(line)[1]] = statusError
		}
		if strings.HasPrefix(line, "ok") && havePrev {
			m[prevTest] = statusPassed
		}
	}
	for _, re := range djangoInterrupted {
		for _, g := range re.FindAllStringSubmatch(log, -1) {
			m[g[1]] = statusPassed
		}
	}
	return m
}

func parseSympy(log string) statusMap {
	m := statusMap{}
	for _, g := range sympyFailureHeader.FindAllStringSubmatch(log, -1) {
		m[g[2]+".py:"+g[3]] = statusFailed
	}
	for _, line := range strings.Split(log, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "test_") {
			continue
		}
		name := strings.Fields(line)[0]
		switch {
		case strings.HasSuffix(line, " E"):
			m[name] = statusError
		case strings.HasSuffix(line, " F"):
			m[name] = statusFailed
		case strings.HasSuffix(line, " ok"):
			m[name] = statusPassed
		}
	}
	return m
}

func parseMatplotlib(log string) statusMap {
	m := statusMap{}
	for _, line := range strings.Split(log, "\n") {
		line = strings.ReplaceAll(line, "MouseButton.LEFT", "1")
		line = strings.ReplaceAll(line, "MouseButton.RIGHT", "3")
		if !startsWithStatus(line) {
			continue
		}
		if strings.HasPrefix(line, statusFailed) {
			line = strings.ReplaceAll(line, " - ", " ")
		}
		f := strings.Fields(line)
		if len(f) <= 1 || isSkipSummary(f[0], f[1]) {
			continue
		}
		m[f[1]] = f[0]
	}
	return m
}

func parseSeaborn(log string) statusMap {
	m := statusMap{}
	for _, line := range strings.Split(log, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		switch {
		case strings.HasPrefix(line, statusFailed):
			m[f[1]] = statusFailed
		case strings.Contains(line, " "+statusPassed+" "):
			if f[1] == statusPassed {
				m[f[0]] = statusPassed
			}
		case strings.HasPrefix(line, statusPassed):
			m[f[1]] = statusPassed
		}
	}
	return m
}
