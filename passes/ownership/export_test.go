package ownership

// ReleaseMetrics exposes what releaseSummaries measured while computing s, for the tests of
// package ownership_test: work units, releases widened by the step cap, and by the count cap.
func ReleaseMetrics(s *Stats) (work, stepCaps, countCaps int) {
	return s.release.work, s.release.stepCaps, s.release.countCaps
}
