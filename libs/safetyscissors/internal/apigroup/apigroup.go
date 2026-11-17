// Package apigroup holds the API version of every record the module writes, so the
// group is spelled in one file. Each is untyped, so a caller's own version type takes it
// as it is.
//
// The records carried magus groups before the module took them (magus.job,
// magus.job-result, magus.step, magus.guard and magus.sessions). No reader accepts
// those: a record stamped with one is refused as any other version is.
package apigroup

// Job is the version of a job row.
const Job = "safetyscissors.job/v1alpha1"

// JobResult is the version of the result a holder files.
const JobResult = "safetyscissors.job-result/v1alpha1"

// Step is the version of a step record.
const Step = "safetyscissors.step/v1alpha1"

// Verdict is the version of the judgment envelope.
const Verdict = "safetyscissors.verdict/v1alpha1"

// Session is the version of a session store record.
const Session = "safetyscissors.session/v1alpha1"
