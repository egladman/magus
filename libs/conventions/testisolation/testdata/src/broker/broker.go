package broker

import "sockdir"

func Dial() string { return sockdir.Dir() }
