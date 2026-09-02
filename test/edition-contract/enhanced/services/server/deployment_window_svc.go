package server

import (
	"errors"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/services/deployment_windows"
)

// deploymentWindowAdmissionService is deliberately thin: the repository owns
// the transaction so policy, current permission, database time and immutable
// decision persistence cannot be split by a caller-side TOCTOU window.
type deploymentWindowAdmissionService struct {
	repository pro_interfaces.DeploymentWindowPolicyRepository
	evaluator  *deployment_windows.Evaluator
}

var _ pro_interfaces.DeploymentWindowAdmissionService = (*deploymentWindowAdmissionService)(nil)

func NewDeploymentWindowAdmissionService(repository pro_interfaces.DeploymentWindowPolicyRepository) pro_interfaces.DeploymentWindowAdmissionService {
	return &deploymentWindowAdmissionService{
		repository: repository,
		evaluator:  deployment_windows.NewEvaluator(deployment_windows.WithTimezoneValidator(validateAdmissionTimezone)),
	}
}

func (s *deploymentWindowAdmissionService) Claim(request pro_interfaces.DeploymentWindowAdmissionRequest) (pro_interfaces.DeploymentWindowAdmissionClaim, error) {
	if s == nil || s.repository == nil || s.evaluator == nil || request.Validate() != nil {
		return pro_interfaces.DeploymentWindowAdmissionClaim{}, db.ErrInvalidOperation
	}
	return s.repository.ClaimDeploymentWindowAdmission(request, s.evaluator.Evaluate)
}

func validateAdmissionTimezone(value string) error {
	if _, err := time.LoadLocation(value); err != nil {
		return errors.New("invalid deployment window timezone")
	}
	return nil
}
