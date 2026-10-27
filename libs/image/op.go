package image

// Op names one image package verb on a Layout or Published. Values match the Buzz method names.
type Op string

const (
	OpAssembleLayout  Op = "assembleLayout"
	OpAssembleIndex   Op = "assembleIndex"
	OpPullLayout      Op = "pullLayout"
	OpPublishLayout   Op = "publishLayout"
	OpAttachArtifacts Op = "attachArtifacts"
)
