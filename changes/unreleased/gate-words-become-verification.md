### Changed

- **The decided gate names are now verification names, with no alias.** `gate_evidence` is `verification_evidence` in job results, records and the job API. `GateStatus` and `GateEvidence` are `VerificationStatus` and `VerificationEvidence`. `--gate-ready` is `--verify-ready`. The rules `push-ungated` and `gate-repeat` are `push-unverified` and `verify-repeat`.
