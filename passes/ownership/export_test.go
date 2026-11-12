package ownership

import "go/types"

// ObserveRelease makes releaseSummaries report its metrics for each package it summarises: work
// units, releases widened by the step cap, and by the count cap. It returns a function that stops
// the observation. Tests that use it must not run in parallel.
func ObserveRelease(f func(pkgPath string, work, stepCaps, countCaps int)) (stop func()) {
	observeRelease = func(pkg *types.Package, m releaseMetrics) {
		f(pkg.Path(), m.work, m.stepCaps, m.countCaps)
	}
	return func() { observeRelease = nil }
}
