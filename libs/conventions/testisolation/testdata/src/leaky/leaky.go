package leaky // want package:"reaches"

import "broker"

func Run() string { return broker.Dial() }
