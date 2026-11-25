package exited // want `exit for gone in exited/exited.go names no helper`

import "strings"

func a(s string) string { return strings.ToUpper(s) }

func b(s string) string { return strings.ToLower(s) }

func use(s string) string { return a(s) + b(s) } // want `b returns one call and has one use site: inline it`
