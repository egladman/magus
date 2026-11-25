package scoped

import "strings"

func old(s string) string { return strings.ToUpper(s) }

func added(s string) string { return strings.ToLower(s) }

func use(s string) string { return old(s) + added(s) } // want `added returns one call`
