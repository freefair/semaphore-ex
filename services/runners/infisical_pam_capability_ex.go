package runners

import (
	"net/http"
	"strings"
)

// RunnerInfisicalPAMHeader acknowledges that the runner applies
// JobData.InfisicalPAM. An older runner ignores the unknown field and would
// connect directly, so the server dispatches routed tasks only to runners that
// send it with value 1 on the current poll.
const RunnerInfisicalPAMHeader = "X-Runner-Infisical-Pam"

// RunnerSupportsInfisicalPAM reports the capability of the polling runner.
func RunnerSupportsInfisicalPAM(header http.Header) bool {
	return strings.TrimSpace(header.Get(RunnerInfisicalPAMHeader)) == "1"
}
