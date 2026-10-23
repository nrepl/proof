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
	all = append(all, interruptChecks()...)
	all = append(all, loadFileChecks()...)
	all = append(all, completionChecks()...)
	all = append(all, lookupChecks()...)
	all = append(all, clientProfileChecks()...)
	// Last, as they may take the server down.
	all = append(all, robustChecks()...)
	return all
}
