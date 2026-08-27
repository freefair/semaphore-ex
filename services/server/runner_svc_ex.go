package server

import (
	"fmt"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/tz"
	"strings"
	"time"
)

const RunnerSecureRegistrationTokenPrefix = db.RunnerSecureRegistrationTokenPrefix

func (s *RunnerServiceImpl) CreateProjectRunner(
	runner db.Runner,
) (newRunner db.Runner, registrationToken string, err error) {
	if runner.ProjectID == nil || *runner.ProjectID <= 0 {
		err = ErrProjectRunnerRequiresProject
		return
	}
	registrationToken, hash := generateRunnerRegistrationToken(runner.RegistrationPolicy)
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

func (s *RunnerServiceImpl) UpdateProjectRunner(current db.Runner, changes db.Runner) (db.Runner, error) {
	if current.ProjectID == nil || *current.ProjectID <= 0 {
		return db.Runner{}, ErrProjectRunnerRequiresProject
	}
	name := strings.TrimSpace(changes.Name)
	if name == "" {
		return db.Runner{}, ErrProjectRunnerNameRequired
	}
	if changes.MaxParallelTasks < 0 {
		return db.Runner{}, ErrProjectRunnerParallelismInvalid
	}
	if err := db.ValidateRunnerTags(changes.Tags); err != nil {
		return db.Runner{}, fmt.Errorf("%w: %v", ErrProjectRunnerTagsInvalid, err)
	}
	current.Name = name
	current.Tags = db.NormalizeRunnerTags(changes.Tags)
	current.IsDefault = changes.IsDefault
	current.Webhook = strings.TrimSpace(changes.Webhook)
	current.MaxParallelTasks = changes.MaxParallelTasks
	if err := db.ValidateRunnerRegistrationPolicyChange(current, changes.RegistrationPolicy); err != nil {
		return db.Runner{}, err
	}
	current.RegistrationPolicy, _ = db.NormalizeRunnerRegistrationPolicy(changes.RegistrationPolicy)
	if err := s.runnerRepo.UpdateRunner(current); err != nil {
		return db.Runner{}, err
	}
	return current, nil
}

func (s *RunnerServiceImpl) SetProjectRunnerActive(runner db.Runner, active bool) error {
	if runner.ProjectID == nil || *runner.ProjectID <= 0 {
		return ErrProjectRunnerRequiresProject
	}
	if active && !runner.IsRegistered() {
		return ErrProjectRunnerUnregistered
	}
	return s.runnerRepo.SetProjectRunnerActive(*runner.ProjectID, runner.ID, active)
}

func (s *RunnerServiceImpl) DeleteProjectRunner(runner db.Runner) error {
	if runner.ProjectID == nil || *runner.ProjectID <= 0 {
		return ErrProjectRunnerRequiresProject
	}
	return s.runnerRepo.DeleteRunner(*runner.ProjectID, runner.ID)
}

func (s *RunnerServiceImpl) ClearProjectRunnerCache(runner db.Runner) error {
	if runner.ProjectID == nil || *runner.ProjectID <= 0 {
		return ErrProjectRunnerRequiresProject
	}
	return s.runnerRepo.ClearRunnerCache(runner)
}

func (s *RunnerServiceImpl) GetProjectRunnerHealth(
	runner db.Runner,
	now time.Time,
	offlineTimeout time.Duration,
) db.RunnerHealth {
	return runner.Health(now, offlineTimeout)
}

func (s *RunnerServiceImpl) GetProjectRunnerHistory(
	projectID int,
	runnerID int,
	params db.RetrieveQueryParams,
) ([]db.RunnerTaskHistoryItem, error) {
	if projectID <= 0 {
		return nil, ErrProjectRunnerRequiresProject
	}
	return s.runnerRepo.GetRunnerTaskHistory(projectID, runnerID, params)
}
