package testkit

import "testing"

func Main(m *testing.M) { m.Run() }

func Isolated(m *testing.M) int { return m.Run() }
