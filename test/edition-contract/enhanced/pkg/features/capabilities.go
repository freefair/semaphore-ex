package features

import (
	"context"
	"errors"
	"fmt"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/common_errors"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

const (
	lifecycleTestRecordLimit = int64(1_000)
	lifecycleTestByteLimit   = int64(256)
)

type capabilityProvider struct {
	repository db.CapabilityRepository
}

// NewCapabilityProvider returns the clean-room enhanced provider.
func NewCapabilityProvider(repository db.CapabilityRepository) pro_interfaces.CapabilityProvider {
	return &capabilityProvider{repository: repository}
}

func (p *capabilityProvider) Resolve(
	_ context.Context,
	request pro_interfaces.CapabilityRequest,
) (pro_interfaces.CapabilitySnapshot, error) {
	if request.At.IsZero() {
		return pro_interfaces.CapabilitySnapshot{}, errors.New("capability resolution time is required")
	}
	config, err := p.repository.GetCapabilityConfig(string(pro_interfaces.CapabilityLifecycleTest))
	if errors.Is(err, db.ErrNotFound) {
		config = db.CapabilityConfig{
			CapabilityID: string(pro_interfaces.CapabilityLifecycleTest),
			State:        string(pro_interfaces.CapabilityStateDisabled),
		}
	} else if err != nil {
		return pro_interfaces.CapabilitySnapshot{}, fmt.Errorf("load capability configuration: %w", err)
	}
	decision, err := resolveLifecycleTestDecision(request, config)
	if err != nil {
		return pro_interfaces.CapabilitySnapshot{}, err
	}
	projectRunners := pro_interfaces.NewCapabilityDecision(
		pro_interfaces.CapabilityProjectRunners,
		pro_interfaces.CapabilityStateActive,
		pro_interfaces.CapabilityReasonActive,
		[]pro_interfaces.CapabilityAccess{
			pro_interfaces.CapabilityAccessRead,
			pro_interfaces.CapabilityAccessWrite,
		},
		nil,
	)
	ldapDecision := pro_interfaces.NewCapabilityDecision(
		pro_interfaces.CapabilityLDAP,
		pro_interfaces.CapabilityStateActive,
		pro_interfaces.CapabilityReasonActive,
		[]pro_interfaces.CapabilityAccess{
			pro_interfaces.CapabilityAccessRead,
			pro_interfaces.CapabilityAccessWrite,
			pro_interfaces.CapabilityAccessExecute,
		},
		nil,
	)
	workflowTriggerDecision := pro_interfaces.NewCapabilityDecision(
		pro_interfaces.CapabilityWorkflowTriggers,
		pro_interfaces.CapabilityStateActive,
		pro_interfaces.CapabilityReasonActive,
		[]pro_interfaces.CapabilityAccess{
			pro_interfaces.CapabilityAccessRead,
			pro_interfaces.CapabilityAccessWrite,
			pro_interfaces.CapabilityAccessExecute,
		},
		nil,
	)
	projectRolesDecision := pro_interfaces.NewCapabilityDecision(
		pro_interfaces.CapabilityProjectRoles,
		pro_interfaces.CapabilityStateActive,
		pro_interfaces.CapabilityReasonActive,
		[]pro_interfaces.CapabilityAccess{
			pro_interfaces.CapabilityAccessRead,
			pro_interfaces.CapabilityAccessWrite,
		},
		nil,
	)
	runtimeSecrets, err := p.resolveRuntimeSecretsDecision(request)
	if err != nil {
		return pro_interfaces.CapabilitySnapshot{}, err
	}
	totpDecision, err := p.resolveTOTPDecision(request)
	if err != nil {
		return pro_interfaces.CapabilitySnapshot{}, err
	}
	return pro_interfaces.NewCapabilitySnapshot(request, []pro_interfaces.CapabilityDecision{
		decision, projectRunners, runtimeSecrets, totpDecision, ldapDecision, workflowTriggerDecision,
		projectRolesDecision,
	}), nil
}

func (p *capabilityProvider) resolveTOTPDecision(
	request pro_interfaces.CapabilityRequest,
) (pro_interfaces.CapabilityDecision, error) {
	config, err := p.repository.GetCapabilityConfig(string(pro_interfaces.CapabilityTOTP))
	if errors.Is(err, db.ErrNotFound) {
		config = db.CapabilityConfig{
			CapabilityID: string(pro_interfaces.CapabilityTOTP),
			State:        string(pro_interfaces.CapabilityStateDisabled),
		}
	} else if err != nil {
		return pro_interfaces.CapabilityDecision{}, fmt.Errorf("load TOTP capability: %w", err)
	}
	state := pro_interfaces.CapabilityState(config.State)
	access := []pro_interfaces.CapabilityAccess{
		pro_interfaces.CapabilityAccessRead,
		pro_interfaces.CapabilityAccessWrite,
		pro_interfaces.CapabilityAccessExecute,
	}
	reason := pro_interfaces.CapabilityReasonCode(state)
	switch state {
	case pro_interfaces.CapabilityStateDisabled:
		access = []pro_interfaces.CapabilityAccess{pro_interfaces.CapabilityAccessRead}
		reason = pro_interfaces.CapabilityReasonDisabledByAdmin
	case pro_interfaces.CapabilityStateShadow:
		reason = pro_interfaces.CapabilityReasonShadow
	case pro_interfaces.CapabilityStateOptional:
		reason = pro_interfaces.CapabilityReasonOptional
	case pro_interfaces.CapabilityStateRequiredSelected:
		reason = pro_interfaces.CapabilityReasonRequiredSelected
	case pro_interfaces.CapabilityStateRequired:
		reason = pro_interfaces.CapabilityReasonRequired
	default:
		return pro_interfaces.CapabilityDecision{}, fmt.Errorf("unsupported stored TOTP capability state %q", config.State)
	}
	return pro_interfaces.NewCapabilityDecision(
		pro_interfaces.CapabilityTOTP, state, reason, access, nil,
	), nil
}

func (p *capabilityProvider) Configure(
	ctx context.Context,
	request pro_interfaces.CapabilityRequest,
	configuration pro_interfaces.CapabilityConfiguration,
) (pro_interfaces.CapabilitySnapshot, error) {
	if request.At.IsZero() {
		return pro_interfaces.CapabilitySnapshot{}, errors.New("capability configuration time is required")
	}
	if !request.IsAdmin {
		decision := pro_interfaces.NewCapabilityDecision(
			configuration.ID,
			pro_interfaces.CapabilityStateInsufficientPermission,
			pro_interfaces.CapabilityReasonInsufficientPermission,
			nil,
			nil,
		)
		return pro_interfaces.CapabilitySnapshot{}, pro_interfaces.CapabilityDeniedError{
			Decision: decision,
			Required: pro_interfaces.CapabilityAccessWrite,
		}
	}
	if configuration.ID != pro_interfaces.CapabilityLifecycleTest &&
		configuration.ID != pro_interfaces.CapabilityRuntimeSecrets {
		return pro_interfaces.CapabilitySnapshot{}, common_errors.NewValidationError(
			fmt.Sprintf("unsupported capability %q", configuration.ID),
		)
	}
	switch configuration.State {
	case pro_interfaces.CapabilityStateActive,
		pro_interfaces.CapabilityStateDisabled,
		pro_interfaces.CapabilityStateReadOnly:
	default:
		return pro_interfaces.CapabilitySnapshot{}, common_errors.NewValidationError(
			fmt.Sprintf("unsupported configured state %q", configuration.State),
		)
	}
	if err := p.repository.SaveCapabilityConfig(db.CapabilityConfig{
		CapabilityID: string(configuration.ID),
		State:        string(configuration.State),
		ExpiresAt:    configuration.ExpiresAt,
		Updated:      request.At,
	}); err != nil {
		return pro_interfaces.CapabilitySnapshot{}, fmt.Errorf("save capability configuration: %w", err)
	}
	return p.Resolve(ctx, request)
}

func (p *capabilityProvider) resolveRuntimeSecretsDecision(
	request pro_interfaces.CapabilityRequest,
) (pro_interfaces.CapabilityDecision, error) {
	config, err := p.repository.GetCapabilityConfig(string(pro_interfaces.CapabilityRuntimeSecrets))
	if errors.Is(err, db.ErrNotFound) {
		config = db.CapabilityConfig{
			CapabilityID: string(pro_interfaces.CapabilityRuntimeSecrets),
			State:        string(pro_interfaces.CapabilityStateActive),
		}
	} else if err != nil {
		return pro_interfaces.CapabilityDecision{}, fmt.Errorf("load runtime secret capability: %w", err)
	}
	state := pro_interfaces.CapabilityState(config.State)
	if state == pro_interfaces.CapabilityStateDisabled {
		return pro_interfaces.NewCapabilityDecision(
			pro_interfaces.CapabilityRuntimeSecrets,
			state,
			pro_interfaces.CapabilityReasonDisabledByAdmin,
			[]pro_interfaces.CapabilityAccess{pro_interfaces.CapabilityAccessRead},
			nil,
		), nil
	}
	if config.ExpiresAt != nil && !request.At.Before(*config.ExpiresAt) {
		return pro_interfaces.NewCapabilityDecision(
			pro_interfaces.CapabilityRuntimeSecrets,
			pro_interfaces.CapabilityStateExpired,
			pro_interfaces.CapabilityReasonEntitlementExpired,
			[]pro_interfaces.CapabilityAccess{pro_interfaces.CapabilityAccessRead},
			nil,
		), nil
	}
	switch state {
	case pro_interfaces.CapabilityStateActive:
		return pro_interfaces.NewCapabilityDecision(
			pro_interfaces.CapabilityRuntimeSecrets,
			state,
			pro_interfaces.CapabilityReasonActive,
			[]pro_interfaces.CapabilityAccess{
				pro_interfaces.CapabilityAccessRead,
				pro_interfaces.CapabilityAccessWrite,
				pro_interfaces.CapabilityAccessExecute,
			},
			nil,
		), nil
	case pro_interfaces.CapabilityStateReadOnly:
		return pro_interfaces.NewCapabilityDecision(
			pro_interfaces.CapabilityRuntimeSecrets,
			state,
			pro_interfaces.CapabilityReasonReadOnly,
			[]pro_interfaces.CapabilityAccess{
				pro_interfaces.CapabilityAccessRead,
				pro_interfaces.CapabilityAccessExecute,
			},
			nil,
		), nil
	default:
		return pro_interfaces.CapabilityDecision{}, fmt.Errorf(
			"unsupported stored runtime secret capability state %q", config.State,
		)
	}
}

func resolveLifecycleTestDecision(
	request pro_interfaces.CapabilityRequest,
	config db.CapabilityConfig,
) (pro_interfaces.CapabilityDecision, error) {
	limits := map[pro_interfaces.LimitID]int64{
		pro_interfaces.LimitLifecycleTestRecords:     lifecycleTestRecordLimit,
		pro_interfaces.LimitLifecycleTestRecordBytes: lifecycleTestByteLimit,
	}
	if pro_interfaces.CapabilityState(config.State) == pro_interfaces.CapabilityStateDisabled {
		return pro_interfaces.NewCapabilityDecision(
			pro_interfaces.CapabilityLifecycleTest,
			pro_interfaces.CapabilityStateDisabled,
			pro_interfaces.CapabilityReasonDisabledByAdmin,
			nil,
			limits,
		), nil
	}
	if config.ExpiresAt != nil && !request.At.Before(*config.ExpiresAt) {
		return pro_interfaces.NewCapabilityDecision(
			pro_interfaces.CapabilityLifecycleTest,
			pro_interfaces.CapabilityStateExpired,
			pro_interfaces.CapabilityReasonEntitlementExpired,
			[]pro_interfaces.CapabilityAccess{pro_interfaces.CapabilityAccessRead},
			limits,
		), nil
	}
	switch pro_interfaces.CapabilityState(config.State) {
	case pro_interfaces.CapabilityStateReadOnly:
		if !request.IsAdmin {
			return insufficientPermissionDecision(limits), nil
		}
		return pro_interfaces.NewCapabilityDecision(
			pro_interfaces.CapabilityLifecycleTest,
			pro_interfaces.CapabilityStateReadOnly,
			pro_interfaces.CapabilityReasonReadOnly,
			[]pro_interfaces.CapabilityAccess{pro_interfaces.CapabilityAccessRead},
			limits,
		), nil
	case pro_interfaces.CapabilityStateActive:
		if !request.IsAdmin {
			return insufficientPermissionDecision(limits), nil
		}
		return pro_interfaces.NewCapabilityDecision(
			pro_interfaces.CapabilityLifecycleTest,
			pro_interfaces.CapabilityStateActive,
			pro_interfaces.CapabilityReasonActive,
			[]pro_interfaces.CapabilityAccess{
				pro_interfaces.CapabilityAccessRead,
				pro_interfaces.CapabilityAccessWrite,
				pro_interfaces.CapabilityAccessExecute,
			},
			limits,
		), nil
	default:
		return pro_interfaces.CapabilityDecision{}, fmt.Errorf("unsupported stored capability state %q", config.State)
	}
}

func insufficientPermissionDecision(limits map[pro_interfaces.LimitID]int64) pro_interfaces.CapabilityDecision {
	return pro_interfaces.NewCapabilityDecision(
		pro_interfaces.CapabilityLifecycleTest,
		pro_interfaces.CapabilityStateInsufficientPermission,
		pro_interfaces.CapabilityReasonInsufficientPermission,
		nil,
		limits,
	)
}

type capabilityTestService struct {
	repository db.CapabilityRepository
}

// NewCapabilityTestService returns the guarded clean-room lifecycle service.
func NewCapabilityTestService(repository db.CapabilityRepository) pro_interfaces.CapabilityTestService {
	return &capabilityTestService{repository: repository}
}

func (s *capabilityTestService) ListRecords(
	_ context.Context,
	snapshot pro_interfaces.CapabilitySnapshot,
) ([]db.CapabilityTestRecord, error) {
	if err := snapshot.Require(pro_interfaces.CapabilityLifecycleTest, pro_interfaces.CapabilityAccessRead); err != nil {
		return nil, err
	}
	return s.repository.GetCapabilityTestRecords()
}

func (s *capabilityTestService) CreateRecord(
	_ context.Context,
	snapshot pro_interfaces.CapabilitySnapshot,
	value string,
) (db.CapabilityTestRecord, error) {
	if err := snapshot.Require(pro_interfaces.CapabilityLifecycleTest, pro_interfaces.CapabilityAccessWrite); err != nil {
		return db.CapabilityTestRecord{}, err
	}
	return s.persist(snapshot, value, "api")
}

func (s *capabilityTestService) RunBackgroundAction(
	_ context.Context,
	snapshot pro_interfaces.CapabilitySnapshot,
	value string,
) (db.CapabilityTestRecord, error) {
	if err := snapshot.Require(pro_interfaces.CapabilityLifecycleTest, pro_interfaces.CapabilityAccessExecute); err != nil {
		return db.CapabilityTestRecord{}, err
	}
	return s.persist(snapshot, value, "worker")
}

func (s *capabilityTestService) persist(
	snapshot pro_interfaces.CapabilitySnapshot,
	value string,
	source string,
) (db.CapabilityTestRecord, error) {
	decision := snapshot.Decision(pro_interfaces.CapabilityLifecycleTest)
	byteLimit, ok := decision.Limit(pro_interfaces.LimitLifecycleTestRecordBytes)
	if !ok {
		return db.CapabilityTestRecord{}, errors.New("lifecycle test record byte limit is missing")
	}
	if len(value) == 0 || int64(len([]byte(value))) > byteLimit {
		return db.CapabilityTestRecord{}, common_errors.NewValidationError(
			fmt.Sprintf("record value must contain 1 to %d bytes", byteLimit),
		)
	}
	records, err := s.repository.GetCapabilityTestRecords()
	if err != nil {
		return db.CapabilityTestRecord{}, fmt.Errorf("count lifecycle test records: %w", err)
	}
	recordLimit, ok := decision.Limit(pro_interfaces.LimitLifecycleTestRecords)
	if !ok {
		return db.CapabilityTestRecord{}, errors.New("lifecycle test record limit is missing")
	}
	if int64(len(records)) >= recordLimit {
		return db.CapabilityTestRecord{}, common_errors.NewValidationError(
			fmt.Sprintf("lifecycle test record limit %d reached", recordLimit),
		)
	}
	record, err := s.repository.CreateCapabilityTestRecord(db.CapabilityTestRecord{
		Value:   value,
		Source:  source,
		Created: snapshot.Request().At,
	})
	if err != nil {
		return db.CapabilityTestRecord{}, fmt.Errorf("persist lifecycle test record: %w", err)
	}
	return record, nil
}

var _ pro_interfaces.CapabilityProvider = (*capabilityProvider)(nil)
var _ pro_interfaces.CapabilityTestService = (*capabilityTestService)(nil)
