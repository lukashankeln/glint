package cli

const (
	ExitOK         = 0 // no violations at configured fail_on severities
	ExitViolations = 1 // one or more violations found (render and parse failures count as violations)
	ExitError      = 2 // pipeline error: bad config, discovery failure, etc.
)

// ViolationsFoundError is returned by lint when violations meet the fail_on
// threshold. Violations were already printed; main.go maps this to ExitViolations.
type ViolationsFoundError struct{}

func (e *ViolationsFoundError) Error() string { return "" }
