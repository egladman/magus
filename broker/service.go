package broker

import (
	"time"

	"github.com/egladman/magus/spells"
)

// ServiceSpec is a shared service as the broker runs it: resolved argv and nothing a
// spell's schema adds for other readers (charms, sources, the near-duplicate audit).
// It is what crosses the wire, so a spell schema change never changes the protocol.
type ServiceSpec struct {
	// Command is the service's argv; Command[0] is the program.
	Command []string
	// Readiness is a probe run until it exits zero; empty means ready once started.
	Readiness []string
	// Stop stops the service gracefully; empty means SIGTERM to its process group.
	Stop []string
	// Idle is how long the broker keeps the service warm after its last dependent
	// releases; zero means the broker's default.
	Idle time.Duration
}

// NewServiceSpec resolves svc to what the broker runs. An Idle the spell spelled wrong
// resolves to zero, the broker's default, as it does for a service hosted in-process.
func NewServiceSpec(svc spells.Service) ServiceSpec {
	spec := ServiceSpec{
		Command:   argv(svc.Command),
		Readiness: argv(svc.Readiness),
		Stop:      argv(svc.Stop),
	}
	if d, err := time.ParseDuration(svc.Idle); err == nil && d > 0 {
		spec.Idle = d
	}
	return spec
}

// Service is spec as the service supervisor takes it.
func (s ServiceSpec) Service() spells.Service {
	svc := spells.Service{
		Command:   command(s.Command),
		Readiness: command(s.Readiness),
		Stop:      command(s.Stop),
	}
	if s.Idle > 0 {
		svc.Idle = s.Idle.String()
	}
	return svc
}

func argv(c spells.Command) []string {
	if c.Bin == "" {
		return nil
	}
	return append([]string{c.Bin}, c.Args...)
}

func command(argv []string) spells.Command {
	if len(argv) == 0 {
		return spells.Command{}
	}
	return spells.Command{Bin: argv[0], Args: append([]string(nil), argv[1:]...)}
}

func (s ServiceSpec) wire() serviceWire {
	return serviceWire{Command: s.Command, Readiness: s.Readiness, Stop: s.Stop, IdleMS: s.Idle.Milliseconds()}
}

func (w serviceWire) spec() ServiceSpec {
	return ServiceSpec{Command: w.Command, Readiness: w.Readiness, Stop: w.Stop, Idle: time.Duration(w.IdleMS) * time.Millisecond}
}
