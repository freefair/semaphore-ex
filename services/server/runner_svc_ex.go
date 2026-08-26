package server

import (
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/tz"
)

func (s *RunnerServiceImpl) CreateProjectRunner(
	runner db.Runner,
) (newRunner db.Runner, registrationToken string, err error) {
	if runner.ProjectID == nil || *runner.ProjectID <= 0 {
		err = ErrProjectRunnerRequiresProject
		return
	}
	registrationToken, hash := generateRunnerRegistrationToken()
	expiresAt := tz.Now().Add(runnerRegistrationTokenTTL)
	runner.Token = ""
	runner.Active = false
	runner.Registered = false
	runner.RegistrationTokenHash = &hash
	runner.RegistrationTokenExpiresAt = &expiresAt
	newRunner, err = s.runnerRepo.CreateRunner(runner)
	if err != nil {
		registrationToken = ""
	}
	return
}
