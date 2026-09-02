package server

import (
	"context"
	"errors"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
)

type globalCredentialRuntimeResolver struct {
	store            db.Store
	cipher           pro_interfaces.GlobalCredentialMaterialDecryptor
	capability       pro_interfaces.CapabilityProvider
	external         pro_interfaces.GlobalCredentialExternalAdapter
	now              func() time.Time
	beforeFinalCheck func()
}

// NewGlobalCredentialRuntimeResolver returns the sole Enhanced execution-time
// resolver. Its result is value-free; material reaches only the supplied task
// injector after the final policy and audit checks succeed.
func NewGlobalCredentialRuntimeResolver(
	store db.Store,
	dependencies ...pro_interfaces.GlobalCredentialRuntimeDependencies,
) pro_interfaces.GlobalCredentialRuntimeResolver {
	resolver := &globalCredentialRuntimeResolver{
		store: store, cipher: util.Config, now: func() time.Time { return time.Now().UTC() },
	}
	if len(dependencies) > 0 {
		dependency := dependencies[0]
		if dependency.Cipher != nil {
			resolver.cipher = dependency.Cipher
		}
		resolver.capability = dependency.Capability
		resolver.external = dependency.External
	}
	return resolver
}

func NewGlobalCredentialRuntimeResolverWithDependencies(
	store db.Store,
	cipher pro_interfaces.GlobalCredentialMaterialDecryptor,
	capability pro_interfaces.CapabilityProvider,
	external pro_interfaces.GlobalCredentialExternalAdapter,
) pro_interfaces.GlobalCredentialRuntimeResolver {
	return NewGlobalCredentialRuntimeResolver(store, pro_interfaces.GlobalCredentialRuntimeDependencies{
		Cipher: cipher, Capability: capability, External: external,
	})
}

func (s *globalCredentialRuntimeResolver) ResolveAndInject(
	ctx context.Context,
	request pro_interfaces.GlobalCredentialResolutionRequest,
	injector pro_interfaces.GlobalCredentialInjector,
) (pro_interfaces.GlobalCredentialResolutionSnapshot, error) {
	now := s.now().UTC()
	snapshot := snapshotForRequest(request, now)
	if request.Validate() != nil || injector == nil || s.store == nil {
		snapshot.Outcome, snapshot.Reason = pro_interfaces.GlobalCredentialResolutionDenied, pro_interfaces.GlobalCredentialResolutionReasonActorRequired
		return snapshot, pro_interfaces.ErrGlobalCredentialResolutionDenied
	}
	if err := s.requireCapability(ctx, now); err != nil {
		snapshot.Outcome, snapshot.Reason = pro_interfaces.GlobalCredentialResolutionDenied, pro_interfaces.GlobalCredentialResolutionReasonCapability
		return s.recordDenied(snapshot)
	}
	state, reason := s.authorize(request, now)
	snapshot = state.snapshot(snapshot, pro_interfaces.GlobalCredentialResolutionDenied, reason, now)
	if reason != pro_interfaces.GlobalCredentialResolutionReasonAllowed {
		return s.recordDenied(snapshot)
	}

	material, providerVersion, resolveErr := s.resolveMaterial(ctx, state.version)
	if resolveErr != nil {
		snapshot.Outcome, snapshot.Reason = pro_interfaces.GlobalCredentialResolutionFailure, pro_interfaces.GlobalCredentialResolutionReasonProviderUnavailable
		return s.recordFailure(snapshot, resolveErr)
	}
	defer zeroGlobalCredentialMaterial(material)

	if s.beforeFinalCheck != nil {
		s.beforeFinalCheck()
	}
	finalState, finalReason := s.authorize(request, s.now().UTC())
	finalSnapshot := finalState.snapshot(snapshot, pro_interfaces.GlobalCredentialResolutionDenied, finalReason, s.now().UTC())
	if finalReason != pro_interfaces.GlobalCredentialResolutionReasonAllowed ||
		finalState.version.Version != state.version.Version ||
		finalState.version.Fingerprint != state.version.Fingerprint {
		if finalReason == pro_interfaces.GlobalCredentialResolutionReasonAllowed {
			finalSnapshot.Reason = pro_interfaces.GlobalCredentialResolutionReasonCredentialUnavailable
		}
		return s.recordDenied(finalSnapshot)
	}
	finalSnapshot.Outcome = pro_interfaces.GlobalCredentialResolutionAllowed
	finalSnapshot.Reason = pro_interfaces.GlobalCredentialResolutionReasonAllowed
	finalSnapshot.ProviderVersion = providerVersion
	if err := s.record(finalSnapshot); err != nil {
		finalSnapshot.Outcome, finalSnapshot.Reason = pro_interfaces.GlobalCredentialResolutionFailure, pro_interfaces.GlobalCredentialResolutionReasonAuditUnavailable
		return finalSnapshot, pro_interfaces.ErrGlobalCredentialAuditUnavailable
	}
	if err := injector.InjectGlobalCredential(ctx, request.Binding.Target, material); err != nil {
		failure := finalSnapshot
		failure.Outcome, failure.Reason, failure.OccurredAt = pro_interfaces.GlobalCredentialResolutionFailure, pro_interfaces.GlobalCredentialResolutionReasonInjectionFailed, s.now().UTC()
		_ = s.record(failure)
		return failure, err
	}
	return finalSnapshot, nil
}

type globalCredentialAuthorizationState struct {
	credential db.GlobalCredential
	grant      db.GlobalCredentialGrant
	version    db.GlobalCredentialVersion
}

func (s *globalCredentialRuntimeResolver) authorize(request pro_interfaces.GlobalCredentialResolutionRequest, now time.Time) (globalCredentialAuthorizationState, pro_interfaces.GlobalCredentialResolutionReason) {
	if request.ActorID <= 0 {
		return globalCredentialAuthorizationState{}, pro_interfaces.GlobalCredentialResolutionReasonActorRequired
	}
	permission, err := s.store.GetTemplatePermissionContext(request.ProjectID, request.TemplateID, request.ActorID)
	if err != nil || !permission.EffectivePermissions.Can(db.CanRunTemplate) {
		return globalCredentialAuthorizationState{}, pro_interfaces.GlobalCredentialResolutionReasonTaskPermission
	}
	if !permission.ProjectPermissions.Can(db.CanConsumeGrantedCredentials) {
		return globalCredentialAuthorizationState{}, pro_interfaces.GlobalCredentialResolutionReasonConsumePermission
	}
	credential, err := s.store.GetGlobalCredential(request.Binding.CredentialID)
	if err != nil || !credential.Enabled {
		return globalCredentialAuthorizationState{}, pro_interfaces.GlobalCredentialResolutionReasonCredentialUnavailable
	}
	version, err := s.store.GetGlobalCredentialVersion(credential.ID, credential.CurrentVersion)
	if err != nil {
		return globalCredentialAuthorizationState{}, pro_interfaces.GlobalCredentialResolutionReasonCredentialUnavailable
	}
	grant, err := s.store.GetGlobalCredentialGrantForProject(credential.ID, request.ProjectID)
	if err != nil || !grant.IsEffectiveAt(request.ProjectID, db.GlobalCredentialGrantOperationConsume, credential.Enabled, now) {
		return globalCredentialAuthorizationState{}, pro_interfaces.GlobalCredentialResolutionReasonGrantUnavailable
	}
	return globalCredentialAuthorizationState{credential: credential, version: version, grant: grant}, pro_interfaces.GlobalCredentialResolutionReasonAllowed
}

func (s *globalCredentialRuntimeResolver) resolveMaterial(ctx context.Context, version db.GlobalCredentialVersion) ([]byte, int, error) {
	switch version.MaterialKind {
	case db.GlobalCredentialMaterialLocalEncrypted:
		if s.cipher == nil || !s.cipher.OptionEncryptionEnabled() {
			return nil, 0, errors.New("local credential encryption unavailable")
		}
		value, err := s.cipher.DecryptOption(version.EncryptedMaterial)
		if err != nil || len(value) == 0 {
			zeroGlobalCredentialMaterial(value)
			return nil, 0, errors.New("local credential decryption failed")
		}
		return value, version.Version, nil
	case db.GlobalCredentialMaterialExternalReference:
		if s.external == nil {
			return nil, 0, errors.New("external credential provider unavailable")
		}
		result, err := s.external.ResolveGlobalCredentialExternal(ctx, version.ExternalReference)
		if err != nil || len(result.Value) == 0 || result.ProviderVersion <= 0 {
			zeroGlobalCredentialMaterial(result.Value)
			return nil, 0, errors.New("external credential resolution failed")
		}
		return result.Value, result.ProviderVersion, nil
	default:
		return nil, 0, errors.New("credential material kind unavailable")
	}
}

func (s *globalCredentialRuntimeResolver) requireCapability(ctx context.Context, now time.Time) error {
	if s.capability == nil {
		return errors.New("credential capability unavailable")
	}
	snapshot, err := s.capability.Resolve(ctx, pro_interfaces.CapabilityRequest{IsAdmin: true, At: now})
	if err != nil {
		return err
	}
	return snapshot.Require(pro_interfaces.CapabilityRuntimeSecrets, pro_interfaces.CapabilityAccessExecute)
}

func snapshotForRequest(request pro_interfaces.GlobalCredentialResolutionRequest, at time.Time) pro_interfaces.GlobalCredentialResolutionSnapshot {
	return pro_interfaces.GlobalCredentialResolutionSnapshot{
		TaskID: request.TaskID, ProjectID: request.ProjectID, ActorID: request.ActorID, RunnerID: request.RunnerID,
		DispatchGeneration: request.DispatchGeneration, Target: request.Binding.Target, CredentialID: request.Binding.CredentialID,
		OccurredAt: at,
	}
}

func (s globalCredentialAuthorizationState) snapshot(base pro_interfaces.GlobalCredentialResolutionSnapshot, outcome pro_interfaces.GlobalCredentialResolutionOutcome, reason pro_interfaces.GlobalCredentialResolutionReason, at time.Time) pro_interfaces.GlobalCredentialResolutionSnapshot {
	base.Outcome, base.Reason, base.OccurredAt = outcome, reason, at
	if s.credential.ID != 0 {
		base.CredentialID, base.GrantID, base.CredentialVersion = s.credential.ID, s.grant.ID, s.version.Version
		base.VersionFingerprint = s.version.Fingerprint
	}
	return base
}

func (s *globalCredentialRuntimeResolver) recordDenied(snapshot pro_interfaces.GlobalCredentialResolutionSnapshot) (pro_interfaces.GlobalCredentialResolutionSnapshot, error) {
	if err := s.record(snapshot); err != nil {
		snapshot.Outcome, snapshot.Reason = pro_interfaces.GlobalCredentialResolutionFailure, pro_interfaces.GlobalCredentialResolutionReasonAuditUnavailable
		return snapshot, pro_interfaces.ErrGlobalCredentialAuditUnavailable
	}
	return snapshot, pro_interfaces.ErrGlobalCredentialResolutionDenied
}

func (s *globalCredentialRuntimeResolver) recordFailure(snapshot pro_interfaces.GlobalCredentialResolutionSnapshot, resolutionErr error) (pro_interfaces.GlobalCredentialResolutionSnapshot, error) {
	if err := s.record(snapshot); err != nil {
		snapshot.Outcome, snapshot.Reason = pro_interfaces.GlobalCredentialResolutionFailure, pro_interfaces.GlobalCredentialResolutionReasonAuditUnavailable
		return snapshot, pro_interfaces.ErrGlobalCredentialAuditUnavailable
	}
	return snapshot, resolutionErr
}

func (s *globalCredentialRuntimeResolver) record(snapshot pro_interfaces.GlobalCredentialResolutionSnapshot) error {
	_, err := s.store.CreateGlobalCredentialUsage(db.GlobalCredentialUsage{
		TaskID: snapshot.TaskID, ProjectID: snapshot.ProjectID, ActorID: snapshot.ActorID, RunnerID: snapshot.RunnerID,
		DispatchGeneration: snapshot.DispatchGeneration, Target: snapshot.Target, CredentialID: snapshot.CredentialID,
		GrantID: snapshot.GrantID, CredentialVersion: snapshot.CredentialVersion, VersionFingerprint: snapshot.VersionFingerprint,
		ProviderVersion: snapshot.ProviderVersion, Outcome: string(snapshot.Outcome), Reason: string(snapshot.Reason), OccurredAt: snapshot.OccurredAt,
	})
	return err
}

func zeroGlobalCredentialMaterial(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

var _ pro_interfaces.GlobalCredentialRuntimeResolver = (*globalCredentialRuntimeResolver)(nil)
