package runners

// normalizeReportedGeneration keeps rolling upgrades compatible without
// weakening reassignment safety. Generation-unaware runners may finish only a
// task's first assignment; any replacement assignment is generation 2 or newer.
func normalizeReportedGeneration(reported int, current int) int {
	if reported == 0 && current == 1 {
		return 1
	}
	return reported
}
