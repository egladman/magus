//go:build !linux && !darwin

package spawn

func maybeForkTree() {}
