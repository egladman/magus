package diag

type Code int

type diagnostic struct {
	code Code
	msg  string
	err  error
}

func (d diagnostic) Error() string { return d.msg }

func Errorf(c Code, format string, args ...any) error { return diagnostic{code: c, msg: format} }

func Wrap(c Code, err error) error { return diagnostic{code: c, err: err} }
