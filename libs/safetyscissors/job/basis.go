package job

import "github.com/egladman/magus/libs/safetyscissors/internal/enum"

// Basis names what a recorded run of a check rests on, ordered by how little of it the
// worker whose check it is can make up. A check goal names the bases it accepts; a run on
// any other basis grades it as a violation, never as a pass.
type Basis string

const (
	// BasisWitnessed is a run the store watched: it checked the command's argv before the
	// caller started it, digested the output through its own writer and read the exit
	// status from the wait of the process the command started ([Store.Witness]).
	BasisWitnessed Basis = "witnessed"
	// BasisAttested is a statement a caller-named [Verifier] accepted the signature of
	// ([Store.Attest]). It is as strong as the custody of the key behind that verifier.
	BasisAttested Basis = "attested"
	// BasisReported is the caller's word ([Store.ReportRun]). It is recorded, and grades
	// only a goal that accepts it.
	BasisReported Basis = "reported"
)

// Bases is the closed set, strongest first.
func Bases() []Basis { return []Basis{BasisWitnessed, BasisAttested, BasisReported} }

func (v Basis) Values() []string { return enum.Set[Basis](Bases()).Strings() }

// Valid reports whether v is one of [Bases] or empty. A goal's accept list refuses the
// empty basis itself; see [Goal.Faults].
func (v Basis) Valid() bool    { return enum.Set[Basis](Bases()).Valid(v) }
func (v Basis) String() string { return enum.String(v) }

// wireBasis spells a basis as a stored record does.
func wireBasis() wireEnum[Basis] { return newWireEnum(Bases()...) }
