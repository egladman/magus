package diag

import "fmt"

type Code int

type Domain struct{}

func (Domain) Errorf(c Code, format string, args ...any) error { return fmt.Errorf(format, args...) }

func Errorf(c Code, format string, args ...any) error { return fmt.Errorf(format, args...) }

func Format(c Code, msg string) string { return msg }
