package checks

import "github.com/nrepl/proof/internal/check"

// All returns every check in the order they run.
func All() []*check.Check {
	var all []*check.Check
	all = append(all, describeChecks()...)
	all = append(all, opChecks()...)
	all = append(all, sessionChecks()...)
	all = append(all, evalChecks()...)
	all = append(all, stdinChecks()...)
	all = append(all, clientProfileChecks()...)
	return all
}
